package dl

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func blob(n int) []byte {
	b := make([]byte, n)
	r := rand.New(rand.NewSource(42))
	r.Read(b)
	return b
}

// fileServer serves data with Range support, like the HF CDN.
func fileServer(t *testing.T, data []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "blob.bin", time.Unix(0, 0), bytes.NewReader(data))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestToSingleConnection(t *testing.T) {
	data := blob(6 << 20)
	srv := fileServer(t, data)
	dst := filepath.Join(t.TempDir(), "out.bin")

	var mu sync.Mutex
	var lastDone, lastTotal int64
	_, err := To(context.Background(), srv.URL+"/blob.bin", dst, Options{
		Connections: 1,
		Progress: func(done, total int64) {
			mu.Lock()
			lastDone, lastTotal = done, total
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("content mismatch: %d vs %d bytes", len(got), len(data))
	}
	mu.Lock()
	defer mu.Unlock()
	if lastDone != int64(len(data)) {
		t.Fatalf("progress ended at %d, want %d", lastDone, len(data))
	}
	if lastTotal != int64(len(data)) {
		t.Fatalf("progress total = %d, want %d", lastTotal, len(data))
	}
}

func TestToParallelChunks(t *testing.T) {
	data := blob(10 << 20) // 10MB over 8 connections
	srv := fileServer(t, data)
	dst := filepath.Join(t.TempDir(), "out.bin")

	if _, err := To(context.Background(), srv.URL+"/blob.bin", dst, Options{Connections: 8}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("parallel download corrupted the file")
	}
}

func TestToLeavesNoPartFile(t *testing.T) {
	srv := fileServer(t, blob(1<<20))
	dst := filepath.Join(t.TempDir(), "out.bin")
	if _, err := To(context.Background(), srv.URL+"/blob.bin", dst, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst + ".part"); !os.IsNotExist(err) {
		t.Fatalf(".part file was left behind: %v", err)
	}
}

func TestToReportsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "out.bin")
	if _, err := To(context.Background(), srv.URL, dst, Options{Connections: 4}); err == nil {
		t.Fatal("expected an error for HTTP 403")
	}
}

func TestToWithoutRangeSupportFallsBack(t *testing.T) {
	data := blob(5 << 20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ignore Range: always stream the whole body.
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.WriteHeader(http.StatusOK)
		w.Write(data)
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "out.bin")
	if _, err := To(context.Background(), srv.URL, dst, Options{Connections: 8}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got, data) {
		t.Fatal("fallback download corrupted the file")
	}
}

func TestCandidatesMirrors(t *testing.T) {
	gh := Candidates("https://github.com/o/r/releases/download/v1/a.zip")
	if len(gh) < 2 {
		t.Fatalf("expected mirrors for github, got %v", gh)
	}
	if gh[0] != "https://github.com/o/r/releases/download/v1/a.zip" {
		t.Fatalf("direct URL must come first: %v", gh)
	}
	found := false
	for _, u := range gh[1:] {
		if strings.HasPrefix(u, "https://ghfast.top/https://github.com/") {
			found = true
		}
	}
	if !found {
		t.Fatalf("ghfast mirror missing: %v", gh)
	}

	hf := Candidates("https://huggingface.co/Xenova/opus-mt-zh-en/resolve/main/config.json")
	found = false
	for _, u := range hf {
		if strings.HasPrefix(u, "https://hf-mirror.com/") {
			found = true
		}
	}
	if !found {
		t.Fatalf("hf mirror missing: %v", hf)
	}

	plain := Candidates("https://example.com/a.bin")
	if len(plain) != 1 {
		t.Fatalf("unexpected mirrors for plain host: %v", plain)
	}
}

func TestToHonoursContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	dst := filepath.Join(t.TempDir(), "out.bin")
	if _, err := To(ctx, srv.URL, dst, Options{Timeout: 5 * time.Second}); err == nil {
		t.Fatal("expected the cancelled context to abort the download")
	}
}
