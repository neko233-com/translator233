// Package api exposes the translation service over HTTP for agents and tools.
package api

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/neko233-com/translator233/internal/translate"
)

//go:embed index.html
var indexHTML []byte

// Server wraps a translate.Service as an HTTP handler.
type Server struct {
	svc     *translate.Service
	version string
}

// New creates a Server.
func New(svc *translate.Service, version string) *Server {
	return &Server{svc: svc, version: version}
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/translate", s.handleTranslate)
	mux.HandleFunc("/v1/languages", s.handleLanguages)
	return withCORS(mux)
}

func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.version})
}

type translateBody struct {
	Text   string `json:"text"`
	Source string `json:"source"`
	Target string `json:"target"`
}

func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	var body translateBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text 不能为空"})
		return
	}
	if body.Target == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target 不能为空"})
		return
	}
	res, err := s.svc.Translate(r.Context(), translate.Request{
		Text: body.Text, Source: body.Source, Target: body.Target,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleLanguages(w http.ResponseWriter, r *http.Request) {
	installed, _ := s.svc.InstalledPacks()
	set := map[string]bool{}
	var list []string
	for _, p := range installed {
		if !set[p.Source] {
			set[p.Source] = true
			list = append(list, p.Source)
		}
		if !set[p.Target] {
			set[p.Target] = true
			list = append(list, p.Target)
		}
	}
	sort.Strings(list)
	writeJSON(w, http.StatusOK, map[string]any{
		"languages": list,
		"packs":     installed,
		"time":      time.Now().Format(time.RFC3339),
	})
}
