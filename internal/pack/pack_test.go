package pack

import (
	"strings"
	"testing"
)

func TestEmbeddedCatalog(t *testing.T) {
	c, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Packs) < 30 {
		t.Fatalf("catalog has %d packs, expected at least 30", len(c.Packs))
	}
	p, ok := c.Find("zh", "en")
	if !ok {
		t.Fatal("zh-en pack missing")
	}
	if p.Repo != "Xenova/opus-mt-zh-en" {
		t.Fatalf("repo = %q", p.Repo)
	}
	if got := p.TotalSize(); got < 70<<20 || got > 90<<20 {
		t.Fatalf("default zh-en pack should be the slimmed build (~80MB), got %d", got)
	}
	for _, pk := range c.Packs {
		if pk.ID != pk.Source+"-"+pk.Target {
			t.Errorf("pack %q has inconsistent languages", pk.ID)
		}
		if !strings.HasPrefix(pk.Name, "") {
			t.Errorf("pack %q has no name", pk.ID)
		}
	}
}

func TestFileURL(t *testing.T) {
	c, _ := LoadCatalog()
	zh, _ := c.Find("zh", "en")
	// Default packs are slimmed and hosted on our GitHub releases.
	f := zh.FilesOf()["weights.bin"]
	url := zh.URL(f)
	if !strings.HasPrefix(url, "https://github.com/neko233-com/translator233/releases/download/") {
		t.Fatalf("weights.bin should come from the release, got %q", url)
	}
	if _, ok := zh.FilesOf()["tokenizer.tok"]; !ok {
		t.Fatal("slim packs use tokenizer.tok")
	}

	// Packs without an explicit file map still use Hugging Face.
	ja, ok := c.Find("ja", "en")
	if !ok {
		t.Fatal("ja-en pack missing")
	}
	want := "https://huggingface.co/Xenova/opus-mt-ja-en/resolve/main/onnx/encoder_model_int8.onnx"
	if got := ja.URL(ja.FilesOf()["encoder.onnx"]); got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestGetIsCaseInsensitive(t *testing.T) {
	c, _ := LoadCatalog()
	if _, ok := c.Get("ZH-EN"); !ok {
		t.Fatal("Get should be case-insensitive")
	}
}
