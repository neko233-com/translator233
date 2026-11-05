package tokenizer

import (
	"testing"
)

const sample = `{
  "model": {
    "type": "Unigram",
    "unk_id": 1,
    "vocab": [
      ["</s>", 0.0],
      ["<unk>", 0.0],
      ["\u2581hello", -1.0],
      ["\u2581world", -1.0],
      ["\u2581", -2.0],
      ["h", -3.0],
      ["e", -3.0],
      ["l", -3.0],
      ["o", -3.0]
    ]
  },
  "added_tokens": []
}`

func TestEncodeDecode(t *testing.T) {
	tok, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if tok.Size() != 9 {
		t.Fatalf("size = %d, want 9", tok.Size())
	}
	ids := tok.Encode("hello world")
	want := []int32{2, 3, 0}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}
	if got := tok.Decode(ids); got != "hello world" {
		t.Fatalf("decode = %q", got)
	}
}

func TestUnknownFallback(t *testing.T) {
	tok, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	// "▁" is known, the three z runes are not.
	ids := tok.Encode("zzz")
	if len(ids) != 5 {
		t.Fatalf("expected metaspace + 3 unknowns + eos, got %v", ids)
	}
	for _, id := range ids[1 : len(ids)-1] {
		if id != tok.Unk() {
			t.Fatalf("id %d is not unk (ids=%v)", id, ids)
		}
	}
}

func TestRejectsOtherModels(t *testing.T) {
	if _, err := Parse([]byte(`{"model":{"type":"BPE","vocab":[]}}`)); err == nil {
		t.Fatal("expected an error for BPE models")
	}
}
