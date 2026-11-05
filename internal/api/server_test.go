package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neko233-com/translator233/internal/pack"
	"github.com/neko233-com/translator233/internal/translate"
)

type fakeTranslator struct {
	last translate.Request
	err  error
}

func (f *fakeTranslator) Translate(_ context.Context, req translate.Request) (*translate.Result, error) {
	f.last = req
	if f.err != nil {
		return nil, f.err
	}
	return &translate.Result{
		Text: "Hello, world.", Source: "zh", Target: req.Target,
		Pack: "zh-en", Engine: "local", Segments: 1, Millis: 12.5,
	}, nil
}

func (f *fakeTranslator) InstalledPacks() ([]pack.Installed, error) {
	return []pack.Installed{
		{Pack: pack.Pack{ID: "zh-en", Source: "zh", Target: "en"}},
		{Pack: pack.Pack{ID: "en-zh", Source: "en", Target: "zh"}},
	}, nil
}

func newServer(t *testing.T) (*Server, *fakeTranslator) {
	t.Helper()
	fake := &fakeTranslator{}
	return New(fake, "test"), fake
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHealth(t *testing.T) {
	srv, _ := newServer(t)
	w := do(t, srv.Handler(), http.MethodGet, "/health", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["ok"] != true || got["version"] != "test" {
		t.Fatalf("unexpected body: %v", got)
	}
}

func TestIndexServesPage(t *testing.T) {
	srv, _ := newServer(t)
	w := do(t, srv.Handler(), http.MethodGet, "/", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), "translator233") {
		t.Fatal("page does not mention the app")
	}
}

func TestTranslateOK(t *testing.T) {
	srv, fake := newServer(t)
	w := do(t, srv.Handler(), http.MethodPost, "/v1/translate", `{"text":"你好","source":"auto","target":"en"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var res translate.Result
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Text != "Hello, world." || res.Pack != "zh-en" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if fake.last.Target != "en" || fake.last.Text != "你好" {
		t.Fatalf("request not forwarded: %+v", fake.last)
	}
}

func TestTranslateValidation(t *testing.T) {
	srv, _ := newServer(t)
	h := srv.Handler()
	cases := []struct {
		name, method, body string
		want               int
	}{
		{"wrong method", http.MethodGet, "", http.StatusMethodNotAllowed},
		{"bad json", http.MethodPost, `{`, http.StatusBadRequest},
		{"empty text", http.MethodPost, `{"text":"  ","target":"en"}`, http.StatusBadRequest},
		{"missing target", http.MethodPost, `{"text":"hi"}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if w := do(t, h, c.method, "/v1/translate", c.body); w.Code != c.want {
				t.Fatalf("status = %d, want %d (%s)", w.Code, c.want, w.Body.String())
			}
		})
	}
}

func TestTranslateReportsServiceError(t *testing.T) {
	fake := &fakeTranslator{err: errTest}
	srv := New(fake, "test")
	w := do(t, srv.Handler(), http.MethodPost, "/v1/translate", `{"text":"hi","target":"xx"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "boom") {
		t.Fatalf("error not surfaced: %s", w.Body.String())
	}
}

func TestLanguages(t *testing.T) {
	srv, _ := newServer(t)
	w := do(t, srv.Handler(), http.MethodGet, "/v1/languages", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body struct {
		Languages []string         `json:"languages"`
		Packs     []pack.Installed `json:"packs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := []string{"en", "zh"}
	if len(body.Languages) != 2 || body.Languages[0] != want[0] || body.Languages[1] != want[1] {
		t.Fatalf("languages = %v, want %v", body.Languages, want)
	}
	if len(body.Packs) != 2 {
		t.Fatalf("packs = %d", len(body.Packs))
	}
}

func TestCORS(t *testing.T) {
	srv, _ := newServer(t)
	h := srv.Handler()
	pre := do(t, h, http.MethodOptions, "/v1/translate", "")
	if pre.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", pre.Code)
	}
	if pre.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("missing CORS header")
	}
	ok := do(t, h, http.MethodPost, "/v1/translate", `{"text":"hi","target":"en"}`)
	if ok.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("CORS header missing on normal response")
	}
}

func TestUnknownPath(t *testing.T) {
	srv, _ := newServer(t)
	if w := do(t, srv.Handler(), http.MethodGet, "/nope", ""); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

type testError struct{}

func (testError) Error() string { return "boom" }

var errTest = testError{}
