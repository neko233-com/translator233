package tokenizer

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// binaryMagic marks the compact vocabulary written by tools/slim_pack.py.
var binaryMagic = [4]byte{'T', '2', '3', 'T'}

const binaryVersion = 1

// LoadDir loads the vocabulary of a language pack, preferring the compact
// tokenizer.tok over the original tokenizer.json.
func LoadDir(dir string) (*Tokenizer, error) {
	if path := filepath.Join(dir, "tokenizer.tok"); fileExists(path) {
		return LoadCompact(path)
	}
	return Load(filepath.Join(dir, "tokenizer.json"))
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Size() > 0
}

// LoadCompact reads the binary vocabulary format:
//
//	magic "T23T" | u32 version | u32 unk id | u32 count
//	count * (u16 len | piece | f32 score)
func LoadCompact(path string) (*Tokenizer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < 16 || raw[0] != binaryMagic[0] || raw[1] != binaryMagic[1] || raw[2] != binaryMagic[2] || raw[3] != binaryMagic[3] {
		return nil, fmt.Errorf("tokenizer: %s is not a compact vocabulary", path)
	}
	version := binary.BigEndian.Uint32(raw[4:8])
	if version != binaryVersion {
		return nil, fmt.Errorf("tokenizer: unsupported compact version %d", version)
	}
	unk := int32(binary.BigEndian.Uint32(raw[8:12]))
	count := int(binary.BigEndian.Uint32(raw[12:16]))

	t := &Tokenizer{
		vocab:   make(map[string]int32, count),
		scores:  make(map[string]float32, count),
		byID:    make([]string, count),
		special: make(map[int32]string, 8),
		unkID:   unk,
	}

	off := 16
	for i := 0; i < count; i++ {
		if off+2 > len(raw) {
			return nil, fmt.Errorf("tokenizer: truncated at %d", off)
		}
		n := int(binary.BigEndian.Uint16(raw[off:]))
		off += 2
		if off+n+4 > len(raw) {
			return nil, fmt.Errorf("tokenizer: truncated piece at %d", off)
		}
		piece := string(raw[off : off+n])
		off += n
		score := math.Float32frombits(binary.BigEndian.Uint32(raw[off:]))
		off += 4

		id := int32(i)
		t.vocab[piece] = id
		t.scores[piece] = score
		t.byID[i] = piece
		if len(piece) > 1 && piece[0] == '<' && piece[len(piece)-1] == '>' {
			t.special[id] = piece
		}
	}
	if id, ok := t.vocab["</s>"]; ok {
		t.eosID = id
	}
	minScore := float32(0)
	first := true
	for _, s := range t.scores {
		if first || s < minScore {
			minScore, first = s, false
		}
	}
	t.unkScore = minScore - 10
	t.trie = buildTrie(t.vocab)
	return t, nil
}
