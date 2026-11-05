// Package translate is the high-level service shared by the CLI, the HTTP API
// and the GUI. It owns the ONNX Runtime, lazily loaded language packs, sentence
// splitting and a result cache.
package translate

import (
	"container/list"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/neko233-com/translator233/internal/appdir"
	"github.com/neko233-com/translator233/internal/lang"
	"github.com/neko233-com/translator233/internal/llm"
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
	// AutoInstall downloads a missing language pack on first use.
	AutoInstall bool
	// Progress reports pack downloads: pack id, current file, bytes, total.
	Progress func(packID, file string, done, total int64)
	// Engine selects the backend: "local" (default), "llm", or "auto"
	// (local first, LLM when no local pack is available).
	Engine string
	// SessionConfig holds extra ONNX Runtime session options.
	SessionConfig map[string]string
	// IdleTimeout unloads a model that has not been used for this long, so a
	// daemon left running drops back to ~80MB. 0 means 5 minutes, negative
	// disables unloading.
	IdleTimeout time.Duration
	// LLM configures the optional OpenAI-compatible backend.
	LLM *llm.Config
}

// Engine names reported in Result.
const (
	EngineLocal = "local"
	EngineLLM   = "llm"
)

// DefaultPacks are installed on first run; every other direction is downloaded
// on demand.
var DefaultPacks = []string{"zh-en", "en-zh"}

// Service translates text with locally installed packs.
type Service struct {
	store      *pack.Store
	catalog    *pack.Catalog
	runtime    string
	threads    int
	maxLd      int
	maxChr     int
	cache      *textCache
	auto       bool
	prog       func(packID, file string, done, total int64)
	engineMode string
	llm        *llm.Config
	sessCfg    map[string]string
	idle       time.Duration
	stopCh     chan struct{}

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
	busy     int
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
		if v, err := strconv.Atoi(os.Getenv("TRANSLATOR233_MAX_MODELS")); err == nil && v > 0 {
			o.MaxLoaded = v
		}
	}
	if o.CacheSize <= 0 {
		o.CacheSize = 1024
	}
	if o.MaxChars <= 0 {
		o.MaxChars = 380
	}
	if o.Engine == "" {
		o.Engine = "local"
	}
	if o.IdleTimeout == 0 {
		o.IdleTimeout = 5 * time.Minute
		if v, err := strconv.Atoi(os.Getenv("TRANSLATOR233_IDLE_SECONDS")); err == nil {
			o.IdleTimeout = time.Duration(v) * time.Second
		}
	}
	// TRANSLATOR233_SESSION_CONFIG="enable_cpu_mem_arena=0;other=..." lets ops
	// trade speed for memory without a rebuild.
	merged := map[string]string{}
	for k, v := range parseSessionConfig(os.Getenv("TRANSLATOR233_SESSION_CONFIG")) {
		merged[k] = v
	}
	for k, v := range o.SessionConfig {
		merged[k] = v
	}
	return &Service{
		store:      o.Store,
		catalog:    o.Catalog,
		runtime:    o.RuntimePath,
		threads:    o.Threads,
		maxLd:      o.MaxLoaded,
		maxChr:     o.MaxChars,
		cache:      newTextCache(o.CacheSize),
		auto:       o.AutoInstall,
		prog:       o.Progress,
		engineMode: o.Engine,
		llm:        o.LLM,
		sessCfg:    merged,
		idle:       o.IdleTimeout,
		engines:    make(map[string]*engineEntry),
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
	Engine   string  `json:"engine"`
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
		return &Result{Text: text, Source: source, Target: target, Pack: "(identity)", Engine: EngineLocal}, nil
	}
	if s.engineMode == "llm" {
		return s.translateLLM(ctx, text, source, target, start)
	}

	steps, err := s.plan(ctx, source, target)
	if err != nil {
		if s.engineMode == "auto" && s.llm != nil && s.llm.Enabled() {
			return s.translateLLM(ctx, text, source, target, start)
		}
		return nil, err
	}
	key := steps[0].id + "\x00" + strings.Join([]string{source, target}, ">") + "\x00" + text
	if cached, ok := s.cache.Get(key); ok {
		return &Result{Text: cached, Source: source, Target: target, Pack: steps[0].id, Engine: EngineLocal,
			Cached: true, Millis: float64(time.Since(start).Microseconds()) / 1000}, nil
	}

	segs := Split(text, s.maxChr)
	for _, st := range steps {
		eng, release, err := s.engine(st.id)
		if err != nil {
			return nil, err
		}
		for i, seg := range segs {
			if strings.TrimSpace(seg) == "" {
				continue
			}
			if err := ctx.Err(); err != nil {
				release()
				return nil, err
			}
			tr, err := eng.Translate(seg)
			if err != nil {
				release()
				return nil, err
			}
			segs[i] = tr
		}
		release()
	}
	joined := joinSegments(segs)
	res := &Result{
		Text:     joined,
		Source:   source,
		Target:   target,
		Pack:     steps[0].id,
		Engine:   EngineLocal,
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

func (s *Service) plan(ctx context.Context, source, target string) ([]step, error) {
	if p, ok := s.catalog.Find(source, target); ok {
		if err := s.ensure(ctx, p); err != nil {
			return nil, err
		}
		return []step{{id: p.ID}}, nil
	}
	if source != "en" && target != "en" {
		a, okA := s.catalog.Find(source, "en")
		b, okB := s.catalog.Find("en", target)
		if okA && okB {
			if err := s.ensure(ctx, a); err != nil {
				return nil, err
			}
			if err := s.ensure(ctx, b); err != nil {
				return nil, err
			}
			return []step{{id: a.ID}, {id: b.ID}}, nil
		}
	}
	return nil, fmt.Errorf("没有 %s → %s 的语言包（可用 pivot: %s → en → %s）", source, target, source, target)
}

// ensure installs a pack when auto-install is enabled.
func (s *Service) ensure(ctx context.Context, p pack.Pack) error {
	if s.store.IsInstalled(p) {
		return nil
	}
	if !s.auto {
		return &ErrNoPack{ID: p.ID}
	}
	return s.InstallPack(ctx, p)
}

// InstallPack downloads one pack, reporting progress through Options.Progress.
func (s *Service) InstallPack(ctx context.Context, p pack.Pack) error {
	if err := s.store.Install(ctx, p, func(file string, done, total int64) {
		if s.prog != nil {
			s.prog(p.ID, file, done, total)
		}
	}); err != nil {
		return fmt.Errorf("安装语言包 %s 失败: %w", p.ID, err)
	}
	return nil
}

// EnsureDefaults installs the default Chinese/English packs when no pack is
// installed yet. Every other direction is fetched on demand.
func (s *Service) EnsureDefaults(ctx context.Context) error {
	if s.HasPacks() {
		return nil
	}
	var wg sync.WaitGroup
	errs := make([]error, len(DefaultPacks))
	for i, id := range DefaultPacks {
		p, ok := s.catalog.Get(id)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(i int, p pack.Pack) {
			defer wg.Done()
			errs[i] = s.InstallPack(ctx, p)
		}(i, p)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// Warm loads up to MaxLoaded installed packs so the first request after a
// daemon start does not pay the ~350ms model load.
func (s *Service) Warm() error {
	installed, err := s.store.List()
	if err != nil {
		return err
	}
	for i, p := range installed {
		if i >= s.maxLd {
			break
		}
		if _, release, err := s.engine(p.ID); err != nil {
			return err
		} else {
			// Keep the model resident; only the idle reaper unloads it.
			release()
		}
	}
	return nil
}

// HasPacks reports whether at least one language pack is installed.
func (s *Service) HasPacks() bool {
	list, err := s.store.List()
	return err == nil && len(list) > 0
}

// engine returns a loaded engine and a release function that must be called
// when the caller stops using it; the idle reaper never unloads a busy engine.
func (s *Service) engine(id string) (*marian.Engine, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.engines[id]
	if !ok {
		if err := s.loadRuntimeLocked(); err != nil {
			return nil, nil, err
		}
		if s.loaded >= s.maxLd {
			s.evictLocked()
		}
		eng, err := marian.Open(s.lib, s.env, s.store.Path(id), marian.Options{Threads: s.threads, Config: s.sessCfg})
		if err != nil {
			return nil, nil, err
		}
		e = &engineEntry{eng: eng}
		s.engines[id] = e
		s.loaded++
		s.startReaperLocked()
	}
	e.busy++
	e.lastUsed = time.Now()
	eng := e.eng
	return eng, func() {
		s.mu.Lock()
		e.busy--
		e.lastUsed = time.Now()
		s.mu.Unlock()
	}, nil
}

// startReaperLocked starts the idle unloader once per service.
func (s *Service) startReaperLocked() {
	if s.idle <= 0 || s.stopCh != nil {
		return
	}
	s.stopCh = make(chan struct{})
	interval := s.idle / 4
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	if interval > 30*time.Second {
		interval = 30 * time.Second
	}
	go func(stop chan struct{}, d time.Duration) {
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				s.reap()
			}
		}
	}(s.stopCh, interval)
}

// shouldUnload reports whether an idle engine may be closed.
func shouldUnload(now, lastUsed time.Time, busy int, idle time.Duration) bool {
	return idle > 0 && busy == 0 && now.Sub(lastUsed) >= idle
}

// reap unloads engines that have been idle for longer than the timeout.
func (s *Service) reap() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idle <= 0 {
		return
	}
	now := time.Now()
	for id, e := range s.engines {
		if !shouldUnload(now, e.lastUsed, e.busy, s.idle) {
			continue
		}
		e.eng.Close()
		delete(s.engines, id)
		s.loaded--
	}
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
	if s.stopCh != nil {
		close(s.stopCh)
		s.stopCh = nil
	}
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

// translateLLM renders text through the OpenAI-compatible backend.
func (s *Service) translateLLM(ctx context.Context, text, source, target string, start time.Time) (*Result, error) {
	if s.llm == nil || !s.llm.Enabled() {
		return nil, fmt.Errorf("未配置在线模型：设置 DEEPSEEK_API_KEY 或运行 translator233 config set-llm")
	}
	key := "llm\x00" + source + ">" + target + "\x00" + text
	if cached, ok := s.cache.Get(key); ok {
		return &Result{Text: cached, Source: source, Target: target, Pack: s.llm.Model, Engine: EngineLLM,
			Cached: true, Millis: float64(time.Since(start).Microseconds()) / 1000}, nil
	}
	chunks := []string{text}
	if len([]rune(text)) > 3000 {
		chunks = Split(text, 3000)
	}
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			out = append(out, chunk)
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tr, err := s.llm.Translate(ctx, chunk, source, target)
		if err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	joined := joinSegments(out)
	s.cache.Add(key, joined)
	return &Result{
		Text:     joined,
		Source:   source,
		Target:   target,
		Pack:     s.llm.Model,
		Engine:   EngineLLM,
		Segments: len(chunks),
		Millis:   float64(time.Since(start).Microseconds()) / 1000,
	}, nil
}

// Engine reports the configured backend.
func (s *Service) Engine() string { return s.engineMode }

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

// parseSessionConfig reads "k=v;k2=v2" into a map.
func parseSessionConfig(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if k, v, ok := strings.Cut(part, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}
