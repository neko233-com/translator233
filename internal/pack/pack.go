// Package pack manages translation language packs: the catalog, installation and
// on-disk layout.
package pack

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/neko233-com/translator233/internal/appdir"
	"github.com/neko233-com/translator233/internal/dl"
)

//go:embed catalog.json
var embeddedCatalog []byte

// File describes one file of a pack.
type File struct {
	From   string `json:"from"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

// Pack is one downloadable translation model.
type Pack struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Name   string `json:"name"`
	Repo   string `json:"repo"`
	Rev    string `json:"rev,omitempty"`
	// Base overrides the Hugging Face host, for mirrors and tests.
	Base  string          `json:"base,omitempty"`
	Files map[string]File `json:"files,omitempty"`
	Size  int64           `json:"size,omitempty"`
}

// Catalog is the set of known packs.
type Catalog struct {
	Updated string `json:"updated"`
	Packs   []Pack `json:"packs"`
}

// DefaultFiles is the Hugging Face Marian ONNX layout used by opus-mt repos.
// The int8 variants are ~120MB per direction and clearly better than the
// "quantized" ones, which is why they are the default.
func DefaultFiles() map[string]File {
	return map[string]File{
		"encoder.onnx":   {From: "onnx/encoder_model_int8.onnx", Size: 52726552},
		"decoder.onnx":   {From: "onnx/decoder_model_merged_int8.onnx", Size: 60013017},
		"tokenizer.json": {From: "tokenizer.json", Size: 6381339},
		"config.json":    {From: "config.json", Size: 1389},
	}
}

// FilesOf returns the file map, applying the default Marian layout.
func (p Pack) FilesOf() map[string]File {
	if len(p.Files) > 0 {
		return p.Files
	}
	return DefaultFiles()
}

// TotalSize estimates the download size in bytes.
func (p Pack) TotalSize() int64 {
	if p.Size > 0 {
		return p.Size
	}
	var n int64
	for _, f := range p.FilesOf() {
		n += f.Size
	}
	return n
}

// URL builds the download URL for one file.
func (p Pack) URL(f File) string {
	rev := p.Rev
	if rev == "" {
		rev = "main"
	}
	base := p.Base
	if base == "" {
		base = "https://huggingface.co"
	}
	return fmt.Sprintf("%s/%s/resolve/%s/%s", strings.TrimSuffix(base, "/"), p.Repo, rev, f.From)
}

// LoadCatalog parses the embedded catalog.
func LoadCatalog() (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(embeddedCatalog, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Get finds a pack by id (case-insensitive).
func (c *Catalog) Get(id string) (Pack, bool) {
	for _, p := range c.Packs {
		if strings.EqualFold(p.ID, id) {
			return p, true
		}
	}
	return Pack{}, false
}

// Find locates the pack for a language pair.
func (c *Catalog) Find(src, tgt string) (Pack, bool) {
	return c.Get(strings.ToLower(src) + "-" + strings.ToLower(tgt))
}

// Store is the installed-pack directory.
type Store struct {
	Dir string
	// Connections is the number of parallel downloads per file (0 = default).
	Connections int
}

// DefaultStore returns the user's pack store.
func DefaultStore() *Store { return &Store{Dir: appdir.Packs()} }

// Installed describes a pack present on disk.
type Installed struct {
	Pack
	Path      string    `json:"path"`
	Installed time.Time `json:"installed"`
	Bytes     int64     `json:"bytes"`
}

// List returns installed packs sorted by id.
func (s *Store) List() ([]Installed, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Installed
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.Dir, e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "pack.json"))
		if err != nil {
			continue
		}
		var meta struct {
			Pack
			Installed time.Time `json:"installed"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}
		var size int64
		for name, f := range meta.Pack.FilesOf() {
			if st, err := os.Stat(filepath.Join(dir, name)); err == nil {
				size += st.Size()
			} else {
				size += f.Size
			}
		}
		out = append(out, Installed{Pack: meta.Pack, Path: dir, Installed: meta.Installed, Bytes: size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// IsInstalled reports whether the pack is complete on disk.
func (s *Store) IsInstalled(p Pack) bool {
	dir := s.Path(p.ID)
	for name := range p.FilesOf() {
		if st, err := os.Stat(filepath.Join(dir, name)); err != nil || st.Size() == 0 {
			return false
		}
	}
	return true
}

// Path returns the directory of a pack.
func (s *Store) Path(id string) string { return filepath.Join(s.Dir, strings.ToLower(id)) }

// Progress reports installation progress.
type Progress func(file string, done, total int64)

// Install downloads every file of the pack.
func (s *Store) Install(ctx context.Context, p Pack, prog Progress) error {
	if err := appdir.Ensure(); err != nil {
		return err
	}
	dir := s.Path(p.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	names := make([]string, 0, len(p.FilesOf()))
	for name := range p.FilesOf() {
		names = append(names, name)
	}
	sort.Strings(names)
	conns := s.Connections
	if conns <= 0 {
		conns = dl.DefaultConnections
	}
	for _, name := range names {
		f := p.FilesOf()[name]
		dst := filepath.Join(dir, name)
		if st, err := os.Stat(dst); err == nil && st.Size() > 0 && (f.Size == 0 || st.Size() == f.Size) {
			continue
		}
		_, err := dl.To(ctx, p.URL(f), dst, dl.Options{
			Timeout:     60e9,
			Connections: conns,
			Progress: func(done, total int64) {
				if prog != nil {
					prog(name, done, total)
				}
			},
		})
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	meta, _ := json.MarshalIndent(struct {
		Pack
		Installed time.Time `json:"installed"`
	}{p, time.Now()}, "", "  ")
	return os.WriteFile(filepath.Join(dir, "pack.json"), meta, 0o644)
}

// Remove deletes an installed pack.
func (s *Store) Remove(id string) error { return os.RemoveAll(s.Path(id)) }
