// Package marian runs ONNX Marian encoder-decoder translation models.
package marian

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"github.com/neko233-com/translator233/internal/ort"
	"github.com/neko233-com/translator233/internal/tokenizer"
)

// Config mirrors the fields of a Marian config.json that the runtime needs.
type Config struct {
	VocabSize      int     `json:"vocab_size"`
	DModel         int     `json:"d_model"`
	DecoderLayers  int     `json:"decoder_layers"`
	DecoderHeads   int     `json:"decoder_attention_heads"`
	MaxLength      int     `json:"max_length"`
	DecoderStartID int     `json:"decoder_start_token_id"`
	EOSID          int     `json:"eos_token_id"`
	PadID          int     `json:"pad_token_id"`
	BadWordsIDs    [][]int `json:"bad_words_ids"`
}

// Options tunes a translation engine.
type Options struct {
	Threads int
	MaxLen  int
}

// Engine is a loaded translation model for one language direction.
type Engine struct {
	lib  *ort.Lib
	env  *ort.Env
	opts *ort.SessionOptions
	enc  *ort.Session
	dec  *ort.Session
	tok  *tokenizer.Tokenizer
	cfg  Config

	mu      sync.Mutex
	scratch [8]float32 // non-nil backing pointer for zero-length tensors
}

// Open loads the ONNX pack stored in dir.
func Open(lib *ort.Lib, env *ort.Env, dir string, o Options) (*Engine, error) {
	cfg, err := loadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil, err
	}
	tok, err := tokenizer.Load(filepath.Join(dir, "tokenizer.json"))
	if err != nil {
		return nil, err
	}
	threads := o.Threads
	if threads <= 0 {
		threads = 4
	}
	so, err := lib.NewSessionOptions(threads)
	if err != nil {
		return nil, err
	}
	var (
		enc, dec *ort.Session
		encErr   error
		decErr   error
		wg       sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		enc, encErr = lib.NewSession(env, filepath.Join(dir, "encoder.onnx"), so)
	}()
	go func() {
		defer wg.Done()
		dec, decErr = lib.NewSession(env, filepath.Join(dir, "decoder.onnx"), so)
	}()
	wg.Wait()
	if encErr != nil {
		if dec != nil {
			dec.Close()
		}
		so.Release(lib)
		return nil, fmt.Errorf("encoder: %w", encErr)
	}
	if decErr != nil {
		enc.Close()
		so.Release(lib)
		return nil, fmt.Errorf("decoder: %w", decErr)
	}
	if o.MaxLen > 0 && o.MaxLen < cfg.MaxLength {
		cfg.MaxLength = o.MaxLen
	}
	return &Engine{lib: lib, env: env, opts: so, enc: enc, dec: dec, tok: tok, cfg: cfg}, nil
}

func loadConfig(path string) (Config, error) {
	var c Config
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("config: %w", err)
	}
	if c.MaxLength <= 0 {
		c.MaxLength = 512
	}
	if c.DModel <= 0 || c.DecoderHeads <= 0 {
		return c, fmt.Errorf("config: missing d_model/decoder_attention_heads")
	}
	return c, nil
}

// Close releases the sessions.
func (e *Engine) Close() {
	if e.enc != nil {
		e.enc.Close()
	}
	if e.dec != nil {
		e.dec.Close()
	}
	if e.opts != nil {
		e.opts.Release(e.lib)
	}
}

// Translate renders text in the target language.
func (e *Engine) Translate(text string) (string, error) {
	ids := e.tok.Encode(text)
	if len(ids) == 0 {
		return "", nil
	}
	if len(ids) > e.cfg.MaxLength {
		ids = append(ids[:e.cfg.MaxLength-1], e.tok.EOS())
	}
	out, err := e.decode(ids)
	if err != nil {
		return "", err
	}
	return e.tok.Decode(out), nil
}

func (e *Engine) decode(src []int32) ([]int32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	encIDs := make([]int64, len(src))
	for i, id := range src {
		encIDs[i] = int64(id)
	}
	mask := make([]int64, len(src))
	for i := range mask {
		mask[i] = 1
	}
	inIDs, err := e.lib.NewTensor(e.enc, ort.TensorInt64, []int64{1, int64(len(encIDs))}, unsafe.Pointer(&encIDs[0]), len(encIDs)*8)
	if err != nil {
		return nil, err
	}
	defer inIDs.Close(e.lib)
	inMask, err := e.lib.NewTensor(e.enc, ort.TensorInt64, []int64{1, int64(len(mask))}, unsafe.Pointer(&mask[0]), len(mask)*8)
	if err != nil {
		return nil, err
	}
	defer inMask.Close(e.lib)

	encIn := make([]*ort.Tensor, len(e.enc.Inputs))
	for i, name := range e.enc.Inputs {
		switch name {
		case "input_ids":
			encIn[i] = inIDs
		case "attention_mask":
			encIn[i] = inMask
		default:
			return nil, fmt.Errorf("encoder: unsupported input %q", name)
		}
	}
	encOuts, err := e.enc.Run(encIn)
	if err != nil {
		return nil, err
	}
	hidden := encOuts[0]
	defer hidden.Close(e.lib)

	past := map[string]*ort.Tensor{}
	encKV := map[string]*ort.Tensor{}
	defer func() {
		for _, t := range past {
			t.Close(e.lib)
		}
		for _, t := range encKV {
			t.Close(e.lib)
		}
	}()

	heads := int64(e.cfg.DecoderHeads)
	headDim := int64(e.cfg.DModel / e.cfg.DecoderHeads)
	shapeKey := []int64{1, heads, 0, headDim}

	cur := int32(e.cfg.DecoderStartID)
	out := make([]int32, 0, 64)
	logitsSize := 0

	for step := 0; step < e.cfg.MaxLength; step++ {
		stepIDs := []int64{int64(cur)}
		idT, err := e.lib.NewTensor(e.dec, ort.TensorInt64, []int64{1, 1}, unsafe.Pointer(&stepIDs[0]), 8)
		if err != nil {
			return nil, err
		}
		branch := []byte{0}
		if step > 0 {
			branch[0] = 1
		}
		branchT, err := e.lib.NewTensor(e.dec, ort.TensorBool, []int64{1}, unsafe.Pointer(&branch[0]), 1)
		if err != nil {
			idT.Close(e.lib)
			return nil, err
		}

		vals := make([]*ort.Tensor, len(e.dec.Inputs))
		owned := make([]*ort.Tensor, 0, 4)
		for i, name := range e.dec.Inputs {
			switch {
			case name == "input_ids":
				vals[i] = idT
			case name == "encoder_attention_mask":
				vals[i] = inMask
			case name == "encoder_hidden_states":
				vals[i] = hidden
			case name == "use_cache_branch":
				vals[i] = branchT
			case strings.HasPrefix(name, "past_key_values."):
				if t, ok := past[name]; ok {
					vals[i] = t
					continue
				}
				empty, err := e.lib.NewTensor(e.dec, ort.TensorFloat32, shapeKey, unsafe.Pointer(&e.scratch[0]), 0)
				if err != nil {
					idT.Close(e.lib)
					branchT.Close(e.lib)
					for _, t := range owned {
						t.Close(e.lib)
					}
					return nil, err
				}
				owned = append(owned, empty)
				vals[i] = empty
			default:
				idT.Close(e.lib)
				branchT.Close(e.lib)
				for _, t := range owned {
					t.Close(e.lib)
				}
				return nil, fmt.Errorf("decoder: unsupported input %q", name)
			}
		}

		outs, err := e.dec.Run(vals)
		idT.Close(e.lib)
		branchT.Close(e.lib)
		for _, t := range owned {
			t.Close(e.lib)
		}
		if err != nil {
			return nil, err
		}

		next := map[string]*ort.Tensor{}
		var logits *ort.Tensor
		for i, name := range e.dec.Outputs {
			switch {
			case name == "logits":
				logits = outs[i]
			case strings.HasPrefix(name, "present."):
				next[strings.Replace(name, "present.", "past_key_values.", 1)] = outs[i]
			default:
				outs[i].Close(e.lib)
			}
		}

		// Cross-attention key/values never change after the first step.
		for name, t := range next {
			if !strings.Contains(name, ".encoder.") {
				continue
			}
			if keep, ok := encKV[name]; ok {
				t.Close(e.lib)
				next[name] = keep
			} else {
				encKV[name] = t
			}
		}
		for name, t := range past {
			if next[name] != t {
				t.Close(e.lib)
			}
		}
		past = next

		if logits == nil {
			return nil, fmt.Errorf("decoder: model produced no logits")
		}
		data, err := logits.Float32(e.lib)
		shape, serr := logits.Shape(e.lib)
		if err != nil || serr != nil {
			logits.Close(e.lib)
			if err == nil {
				err = serr
			}
			return nil, err
		}
		logitsSize = int(shape[len(shape)-1])
		base := len(data) - logitsSize
		best := int32(0)
		bestV := float32(-3.4e38)
		for i := 0; i < logitsSize; i++ {
			if v := data[base+i]; v > bestV {
				bestV = v
				best = int32(i)
			}
		}
		logits.Close(e.lib)

		if best == int32(e.cfg.EOSID) {
			break
		}
		out = append(out, best)
		cur = best
	}
	return out, nil
}
