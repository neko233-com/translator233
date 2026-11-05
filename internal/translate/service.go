// Package translate is the high-level service shared by the CLI, the HTTP API
// and the GUI. It owns the ONNX Runtime, lazily loaded language packs, sentence
// splitting and a result cache.
package translate

import (
	"container/list"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/neko233-com/translator233/internal/appdir"
	"github.com/neko233-com/translator233/internal/lang"
	"github.com/neko233-com/translator233/internal/marian"
	"github.com/neko233-com/translator233/internal/ort"
	"github.com/neko233-com/translator233/internal/pack"
)

// Options configures a Service.
type Options struct {
	Store       *pack.Store
	Catalog     *pack.Catalog
	RuntimePath string
	Threads     int
	MaxLoaded   int
	CacheSize   int
	MaxChars    int
}

// Service translates text with locally installed packs.
type Service struct {
	store   *pack.Store
	catalog *pack.Catalog
	runtime string
	threads int
	maxLd   int
	maxChr  int
	cache   *textCache

	mu      sync.Mutex
	lib     *ort.Lib
	env     *ort.Env
	engines map[string]*engineEntry
	loaded  int
	closed  bool
}

type engineEntry struct {
	eng      *marian.Engine
	lastUsed time.Time
}

// New creates a service. The ONNX Runtime library is loaded on first use.
func New(o Options) (*Service, error) {
	if o.Store == nil {
		o.Store = pack.DefaultStore()
	}
	if o.Catalog == nil {
		c, err := pack.LoadCatalog()
		if err != nil {
			return nil, err
		}
		o.Catalog = c
	}
	if o.RuntimePath == "" {
		o.RuntimePath = appdir.Runtime()
	}
	if o.Threads <= 0 {
		o.Threads = defaultThreads()
	}
	if o.MaxLoaded <= 0 {
		o.MaxLoaded = 2
	}
	if o.CacheSize <= 0 {
		o.CacheSize = 1024
	}
	if o.MaxChars <= 0 {
		o.MaxChars = 380
	}
	return &Service{
		store:   o.Store,
		catalog: o.Catalog,
		runtime: o.RuntimePath,
		threads: o.Threads,
		maxLd:   o.MaxLoaded,
		maxChr:  o.MaxChars,
		cache:   newTextCache(o.CacheSize),
		engines: make(map[string]*engineEntry),
	}, nil
}

// Request is one translation job.
type Request struct {
	Text   string `json:"text"`
	Source string `json:"source"`
	Target string `json:"target"`
}

// Result is one translation response.
type Result struct {
	Text     string  `json:"text"`
	Source   string  `json:"source"`
	Target   string  `json:"target"`
	Pack     string  `json:"pack"`
	Segments int     `json:"segments"`
	Cached   bool    `json:"cached"`
	Millis   float64 `json:"ms"`
}

// ErrNoPack reports that a required language pack is not installed.
type ErrNoPack struct{ ID string }

func (e *ErrNoPack) Error() string {
	return fmt.Sprintf("语言包 %s 未安装，请先运行: translator233 packs install %s", e.ID, e.ID)
}

// Translate runs a request.
func (s *Service) Translate(ctx context.Context, req Request) (*Result, error) {
	start := time.Now()
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return &Result{Source: req.Source, Target: req.Target}, nil
	}
	target := strings.ToLower(strings.TrimSpace(req.Target))
	if target == "" {
		return nil, fmt.Errorf("必须指定目标语言 (-t)")
	}
	source := strings.ToLower(strings.TrimSpace(req.Source))
	if source == "" || source == "auto" {
		source = lang.Detect(text)
		if source == "" {
			source = "en"
		}
	}
	if source == target {
		return &Result{Text: text, Source: source, Target: target, Pack: "(identity)"}, nil
	}

	steps, err := s.plan(source, target)
	if err != nil {
		return nil, err
	}
	key := steps[0].id + "\x00" + strings.Join([]string{source, target}, ">") + "\x00" + text
	if cached, ok := s.cache.Get(key); ok {
		return &Result{Text: cached, Source: source, Target: target, Pack: steps[0].id,
			Cached: true, Millis: float64(time.Since(start).Microseconds()) / 1000}, nil
	}

	segs := Split(text, s.maxChr)
	for _, st := range steps {
		eng, err := s.engine(st.id)
		if err != nil {
			return nil, err
		}
		for i, seg := range segs {
			if strings.TrimSpace(seg) == "" {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			tr, err := eng.Translate(seg)
			if err != nil {
				return nil, err
			}
			segs[i] = tr
		}
	}
	joined := joinSegments(segs)
	res := &Result{
		Text:     joined,
		Source:   source,
		Target:   target,
		Pack:     steps[0].id,
		Segments: len(segs),
		Millis:   float64(time.Since(start).Microseconds()) / 1000,
	}
	if len(steps) > 1 {
		res.Pack = steps[0].id + " → " + steps[1].id
	}
	s.cache.Add(key, joined)
	return res, nil
}

type step struct{ id string }

func (s *Service) plan(source, target string) ([]step, error) {
	if p, ok := s.catalog.Find(source, target); ok {
		if !s.store.IsInstalled(p) {
			return nil, &ErrNoPack{ID: p.ID}
		}
		return []step{{id: p.ID}}, nil
	}
	if source != "en" && target != "en" {
		a, okA := s.catalog.Find(source, "en")
		b, okB := s.catalog.Find("en", target)
		if okA && okB {
			if !s.store.IsInstalled(a) {
				return nil, &ErrNoPack{ID: a.ID}
			}
			if !s.store.IsInstalled(b) {
				return nil, &ErrNoPack{ID: b.ID}
			}
			return []step{{id: a.ID}, {id: b.ID}}, nil
		}
	}
	return nil, fmt.Errorf("没有 %s → %s 的语言包（可用 pivot: %s → en → %s）", source, target, source, target)
}

func (s *Service) engine(id string) (*marian.Engine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.engines[id]; ok {
		e.lastUsed = time.Now()
		return e.eng, nil
	}
	if err := s.loadRuntimeLocked(); err != nil {
		return nil, err
	}
	if s.loaded >= s.maxLd {
		s.evictLocked()
	}
	eng, err := marian.Open(s.lib, s.env, s.store.Path(id), marian.Options{Threads: s.threads})
	if err != nil {
		return nil, err
	}
	s.engines[id] = &engineEntry{eng: eng, lastUsed: time.Now()}
	s.loaded++
	return eng, nil
}

func (s *Service) loadRuntimeLocked() error {
	if s.lib != nil {
		return nil
	}
	path := s.runtime
	if path == "" || !strings.HasSuffix(strings.ToLower(path), libExt()) {
		// s.runtime is the runtime directory: find the library inside it.
		found, err := findLibrary(path)
		if err != nil {
			return err
		}
		path = found
	}
	lib, err := ort.Load(path)
	if err != nil {
		return err
	}
	env, err := lib.NewEnv(ort.LogError, "translator233")
	if err != nil {
		return err
	}
	s.lib, s.env = lib, env
	return nil
}

func (s *Service) evictLocked() {
	var oldestID string
	var oldest time.Time
	for id, e := range s.engines {
		if oldestID == "" || e.lastUsed.Before(oldest) {
			oldestID, oldest = id, e.lastUsed
		}
	}
	if oldestID == "" {
		return
	}
	e := s.engines[oldestID]
	if s.env != nil {
		e.eng.Close()
	}
	delete(s.engines, oldestID)
	s.loaded--
}

// Close releases all engines and the runtime.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for id, e := range s.engines {
		e.eng.Close()
		delete(s.engines, id)
	}
	if s.env != nil {
		s.env.Release(s.lib)
		s.env = nil
	}
}

// Split breaks text into chunks no longer than max runes, preferring sentence
// boundaries and keeping paragraph structure.
func Split(text string, max int) []string {
	if max <= 0 {
		max = 380
	}
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, line := range strings.Split(text, "\n") {
		for _, sentence := range splitSentences(line) {
			if cur.Len()+len(sentence) > max && cur.Len() > 0 {
				flush()
			}
			cur.WriteString(sentence)
		}
		flush()
	}
	flush()
	if len(out) == 0 {
		out = append(out, text)
	}
	return out
}

func splitSentences(line string) []string {
	var out []string
	var b strings.Builder
	runes := []rune(line)
	for i, r := range runes {
		b.WriteRune(r)
		if isTerminator(r) {
			// Keep trailing quotes/brackets with the sentence.
			for i+1 < len(runes) && isCloser(runes[i+1]) {
				i++
				b.WriteRune(runes[i])
			}
			out = append(out, b.String())
			b.Reset()
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

func isTerminator(r rune) bool {
	switch r {
	case '。', '！', '？', '.', '!', '?', ';', '；', '…':
		return true
	}
	return false
}

func isCloser(r rune) bool {
	switch r {
	case '"', '\'', '”', '’', ')', '）', ']', '】', '》':
		return true
	}
	return r == ' ' || r == '\t'
}

func joinSegments(translated []string) string {
	var b strings.Builder
	for i, s := range translated {
		if i > 0 && needsSpace(translated[i-1], s) {
			b.WriteByte(' ')
		}
		b.WriteString(strings.TrimSpace(s))
	}
	return b.String()
}

func needsSpace(prev, next string) bool {
	if prev == "" || next == "" {
		return false
	}
	pr := []rune(prev)
	nr := []rune(next)
	last := pr[len(pr)-1]
	first := nr[0]
	if unicode.IsSpace(last) || unicode.IsSpace(first) {
		return false
	}
	if isCJKPunct(last) || isCJKPunct(first) {
		return false
	}
	return true
}

func isCJKPunct(r rune) bool {
	switch r {
	case '。', '，', '、', '！', '？', '；', '：', '）', '」', '』':
		return true
	}
	return false
}

// InstalledPacks lists packs available on disk.
func (s *Service) InstalledPacks() ([]pack.Installed, error) { return s.store.List() }

// Catalog exposes the pack catalog.
func (s *Service) Catalog() *pack.Catalog { return s.catalog }

// Store exposes the pack store.
func (s *Service) Store() *pack.Store { return s.store }

// --- simple LRU text cache ---

type textCache struct {
	cap int
	mu  sync.Mutex
	ll  *list.List
	it  map[string]*list.Element
}

type cacheItem struct{ key, val string }

func newTextCache(capacity int) *textCache {
	return &textCache{cap: capacity, ll: list.New(), it: make(map[string]*list.Element, capacity)}
}

func (c *textCache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.it[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*cacheItem).val, true
	}
	return "", false
}

func (c *textCache) Add(key, val string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.it[key]; ok {
		el.Value.(*cacheItem).val = val
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&cacheItem{key: key, val: val})
	c.it[key] = el
	for c.ll.Len() > c.cap {
		last := c.ll.Back()
		if last == nil {
			break
		}
		c.ll.Remove(last)
		delete(c.it, last.Value.(*cacheItem).key)
	}
}
