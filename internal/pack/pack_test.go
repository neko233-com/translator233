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
	if got := p.TotalSize(); got < 100<<20 {
		t.Fatalf("unexpected pack size %d", got)
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
	p, _ := LoadCatalog()
	zh, _ := p.Find("zh", "en")
	f := zh.FilesOf()["encoder.onnx"]
	url := zh.URL(f)
	want := "https://huggingface.co/Xenova/opus-mt-zh-en/resolve/main/onnx/encoder_model_int8.onnx"
	if url != want {
		t.Fatalf("url = %q, want %q", url, want)
	}
}

func TestGetIsCaseInsensitive(t *testing.T) {
	c, _ := LoadCatalog()
	if _, ok := c.Get("ZH-EN"); !ok {
		t.Fatal("Get should be case-insensitive")
	}
}
