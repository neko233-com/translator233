package pack

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func serve(mux *http.ServeMux, path string, body []byte) {
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, filepath.Base(path), time.Unix(1_700_000_000, 0), bytes.NewReader(body))
	})
}

// testPack registers its files on mux and returns a pack pointing at srv.
func testPack(mux *http.ServeMux) Pack {
	big := make([]byte, 5<<20)
	rand.Read(big)
	tok := []byte(`{"model":{"type":"Unigram","vocab":[["</s>",0],["<unk>",0],["\u2581hi",-1]]}}`)
	cfg := []byte(`{"d_model":512,"decoder_attention_heads":8,"decoder_layers":6}`)
	serve(mux, "/repo/resolve/main/encoder.onnx", big)
	serve(mux, "/repo/resolve/main/decoder.onnx", big)
	serve(mux, "/repo/resolve/main/tokenizer.json", tok)
	serve(mux, "/repo/resolve/main/config.json", cfg)
	return Pack{
		ID: "xx-yy", Source: "xx", Target: "yy", Name: "测试语向", Repo: "repo",
		Files: map[string]File{
			"encoder.onnx":   {From: "encoder.onnx"},
			"decoder.onnx":   {From: "decoder.onnx"},
			"tokenizer.json": {From: "tokenizer.json"},
			"config.json":    {From: "config.json"},
		},
	}
}

func TestInstallListRemove(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := testPack(mux)
	p.Base = srv.URL
	store := &Store{Dir: filepath.Join(t.TempDir(), "packs"), Connections: 4}

	if store.IsInstalled(p) {
		t.Fatal("pack should not be installed yet")
	}

	done := 0
	if err := store.Install(context.Background(), p, func(name string, got, total int64) {
		if total > 0 && got == total {
			done++
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !store.IsInstalled(p) {
		t.Fatal("pack should be installed")
	}
	if done < len(p.Files) {
		t.Fatalf("progress completed for %d files, want %d", done, len(p.Files))
	}

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "xx-yy" {
		t.Fatalf("unexpected list: %+v", list)
	}
	if list[0].Bytes <= 0 {
		t.Fatalf("installed size not reported: %+v", list[0])
	}

	// Reinstalling an intact pack must not download again.
	if err := store.Install(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}

	if err := store.Remove("xx-yy"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.Path("xx-yy")); !os.IsNotExist(err) {
		t.Fatal("pack directory was not removed")
	}
}

func TestInstallReportsMissingFile(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	p := Pack{
		ID: "a-b", Source: "a", Target: "b", Repo: "repo", Base: srv.URL,
		Files: map[string]File{"encoder.onnx": {From: "encoder.onnx"}},
	}
	store := &Store{Dir: filepath.Join(t.TempDir(), "packs")}
	if err := store.Install(context.Background(), p, nil); err == nil {
		t.Fatal("expected an error when the file is missing")
	}
}
