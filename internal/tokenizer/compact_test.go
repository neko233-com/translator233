package tokenizer

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func writeCompact(t *testing.T, pieces []string, scores []float32, unk int32) string {
	t.Helper()
	buf := make([]byte, 0, 64)
	buf = append(buf, 'T', '2', '3', 'T')
	var hdr [12]byte
	binary.BigEndian.PutUint32(hdr[0:], binaryVersion)
	binary.BigEndian.PutUint32(hdr[4:], uint32(unk))
	binary.BigEndian.PutUint32(hdr[8:], uint32(len(pieces)))
	buf = append(buf, hdr[:]...)
	for i, p := range pieces {
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(p)))
		buf = append(buf, l[:]...)
		buf = append(buf, p...)
		var s [4]byte
		binary.BigEndian.PutUint32(s[:], math.Float32bits(scores[i]))
		buf = append(buf, s[:]...)
	}
	path := filepath.Join(t.TempDir(), "tokenizer.tok")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCompactRoundTrip(t *testing.T) {
	pieces := []string{"</s>", "<unk>", "\u2581hello", "\u2581world", "\u2581", "h", "e", "l", "o"}
	scores := []float32{0, 0, -1, -1, -2, -3, -3, -3, -3}
	path := writeCompact(t, pieces, scores, 1)

	tok, err := LoadCompact(path)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Size() != len(pieces) {
		t.Fatalf("size = %d, want %d", tok.Size(), len(pieces))
	}
	if tok.EOS() != 0 || tok.Unk() != 1 {
		t.Fatalf("eos=%d unk=%d", tok.EOS(), tok.Unk())
	}
	ids := tok.Encode("hello world")
	if len(ids) != 3 || ids[0] != 2 || ids[1] != 3 || ids[2] != 0 {
		t.Fatalf("ids = %v", ids)
	}
	if got := tok.Decode(ids); got != "hello world" {
		t.Fatalf("decode = %q", got)
	}
}

func TestCompactMatchesJSON(t *testing.T) {
	// The same vocabulary must tokenize identically in both formats.
	tok, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	pieces := []string{"</s>", "<unk>", "\u2581hello", "\u2581world", "\u2581", "h", "e", "l", "o"}
	scores := make([]float32, len(pieces))
	for i, p := range pieces {
		scores[i] = tok.scores[p]
	}
	compact, err := LoadCompact(writeCompact(t, pieces, scores, 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"hello world", "hello", "zzz", ""} {
		a, b := tok.Encode(text), compact.Encode(text)
		if len(a) != len(b) {
			t.Fatalf("%q: %v vs %v", text, a, b)
		}
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("%q: %v vs %v", text, a, b)
			}
		}
	}
}

func TestLoadDirPrefersCompact(t *testing.T) {
	dir := t.TempDir()
	pieces := []string{"</s>", "<unk>", "\u2581hi"}
	scores := []float32{0, 0, -1}
	src := writeCompact(t, pieces, scores, 1)
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokenizer.tok"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	// A broken JSON file must be ignored because the compact one wins.
	if err := os.WriteFile(filepath.Join(dir, "tokenizer.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	tok, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir should prefer tokenizer.tok: %v", err)
	}
	if tok.Size() != 3 {
		t.Fatalf("size = %d", tok.Size())
	}
}

func TestLoadCompactRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.tok")
	if err := os.WriteFile(path, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCompact(path); err == nil {
		t.Fatal("expected an error for a non-compact file")
	}
}
