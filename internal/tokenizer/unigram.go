// Package tokenizer implements the SentencePiece Unigram tokenizer as exported
// by Hugging Face (tokenizer.json), which is what the ONNX Marian packs use.
package tokenizer

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

// Tokenizer tokenizes text with a Unigram vocabulary.
type Tokenizer struct {
	vocab  map[string]int32
	scores map[string]float32
	trie   *trie
	unkID  int32
	eosID  int32
	maxLen int
	byID   []string
	unkScore float32
	special map[int32]string
}

type tokenizerJSON struct {
	Model struct {
		Type       string     `json:"type"`
		UnkID      int32      `json:"unk_id"`
		Vocab      [][]any    `json:"vocab"`
	} `json:"model"`
	PostProcessor json.RawMessage `json:"post_processor"`
	AddedTokens   []struct {
		ID      int32  `json:"id"`
		Content string `json:"content"`
	} `json:"added_tokens"`
}

// Load reads a Hugging Face tokenizer.json file.
func Load(path string) (*Tokenizer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(raw)
}

// Parse builds a Tokenizer from tokenizer.json content.
func Parse(raw []byte) (*Tokenizer, error) {
	var tj tokenizerJSON
	if err := json.Unmarshal(raw, &tj); err != nil {
		return nil, fmt.Errorf("tokenizer: %w", err)
	}
	if tj.Model.Type != "Unigram" {
		return nil, fmt.Errorf("tokenizer: unsupported model type %q", tj.Model.Type)
	}
	t := &Tokenizer{
		vocab:   make(map[string]int32, len(tj.Model.Vocab)),
		scores:  make(map[string]float32, len(tj.Model.Vocab)),
		unkID:   tj.Model.UnkID,
		eosID:   0,
		special: make(map[int32]string, 8),
	}
	for _, entry := range tj.Model.Vocab {
		if len(entry) != 2 {
			continue
		}
		piece, ok := entry[0].(string)
		if !ok {
			continue
		}
		score, _ := entry[1].(float64)
		id := int32(len(t.vocab))
		t.vocab[piece] = id
		t.scores[piece] = float32(score)
		if utf8.RuneCountInString(piece) > t.maxLen {
			t.maxLen = utf8.RuneCountInString(piece)
		}
		if strings.HasPrefix(piece, "<") && strings.HasSuffix(piece, ">") {
			t.special[id] = piece
		}
	}
	for _, at := range tj.AddedTokens {
		if _, ok := t.vocab[at.Content]; !ok {
			t.vocab[at.Content] = at.ID
			t.special[at.ID] = at.Content
		}
	}
	if id, ok := t.vocab["</s>"]; ok {
		t.eosID = id
	}
	// SentencePiece penalizes unknown pieces with min_score - kUnkPenalty.
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

// EOS returns the end-of-sequence id from the vocabulary.
func (t *Tokenizer) EOS() int32 { return t.eosID }

// Unk returns the unknown token id.
func (t *Tokenizer) Unk() int32 { return t.unkID }

// Size returns the vocabulary size.
func (t *Tokenizer) Size() int { return len(t.vocab) }

const metaspace = '\u2581'

// Encode converts text to token ids, appending the end-of-sequence token.
func (t *Tokenizer) Encode(text string) []int32 {
	ids := make([]int32, 0, len(text)/2+4)
	for _, word := range strings.Fields(text) {
		t.encodeWord(word, &ids)
	}
	return append(ids, t.eosID)
}

func (t *Tokenizer) encodeWord(word string, out *[]int32) {
	// Metaspace pre-tokenizer: prepend U+2581 to every word.
	s := string(metaspace) + word
	runes := []rune(s)
	n := len(runes)
	const (
		negInf = float32(-1e30)
	)
	best := make([]float32, n+1)
	from := make([]int, n+1)
	piece := make([]int32, n+1)
	for i := 1; i <= n; i++ {
		best[i] = negInf
		from[i] = -1
	}
	for i := 0; i < n; i++ {
		if best[i] == negInf {
			continue
		}
		node := t.trie
		for j := i; j < n; j++ {
			next, ok := node.child[runes[j]]
			if !ok {
				break
			}
			node = next
			if id, isWord := node.id, node.term; isWord {
				sc := best[i] + t.scores[string(runes[i:j+1])]
				if sc > best[j+1] {
					best[j+1] = sc
					from[j+1] = i
					piece[j+1] = id
				}
			}
		}
		// Unknown single rune fallback keeps the algorithm unconditional.
		sc := best[i] + t.unkScore
		if sc > best[i+1] {
			best[i+1] = sc
			from[i+1] = i
			piece[i+1] = t.unkID
		}
	}
	start := n
	ids := make([]int32, 0, n)
	for start > 0 {
		ids = append(ids, piece[start])
		start = from[start]
	}
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	*out = append(*out, ids...)
}

// Decode converts token ids back to text.
func (t *Tokenizer) Decode(ids []int32) string {
	var b strings.Builder
	for _, id := range ids {
		if id == t.eosID {
			continue
		}
		if s, ok := t.special[id]; ok {
			b.WriteString(s)
			continue
		}
		piece, ok := t.piece(id)
		if !ok {
			continue
		}
		b.WriteString(piece)
	}
	out := strings.ReplaceAll(b.String(), string(metaspace), " ")
	return strings.TrimSpace(out)
}

func (t *Tokenizer) piece(id int32) (string, bool) {
	if t.byID == nil {
		t.byID = make([]string, len(t.vocab))
		for p, i := range t.vocab {
			if int(i) < len(t.byID) {
				t.byID[i] = p
			}
		}
	}
	if int(id) < 0 || int(id) >= len(t.byID) {
		return "", false
	}
	p := t.byID[id]
	return p, p != ""
}

type trie struct {
	child map[rune]*trie
	term  bool
	id    int32
}

func buildTrie(vocab map[string]int32) *trie {
	root := &trie{child: make(map[rune]*trie, 64)}
	keys := make([]string, 0, len(vocab))
	for k := range vocab {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "" || strings.HasPrefix(k, "<") {
			continue
		}
		node := root
		for _, r := range k {
			next, ok := node.child[r]
			if !ok {
				next = &trie{child: map[rune]*trie{}}
				node.child[r] = next
			}
			node = next
		}
		node.term = true
		node.id = vocab[k]
	}
	return root
}
