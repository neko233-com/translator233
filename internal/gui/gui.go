// Package gui holds the translator233 desktop window: the state, the view and
// the glue to the translation service. Keeping it out of package main lets the
// layout be tested headless with ui.NewTester.
package gui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/translator233/internal/lang"
	"github.com/neko233-com/translator233/internal/llm"
	"github.com/neko233-com/translator233/internal/pack"
	"github.com/neko233-com/translator233/internal/rtsetup"
	"github.com/neko233-com/translator233/internal/translate"
)

// Translator is the part of translate.Service the window uses.
type Translator interface {
	Translate(ctx context.Context, req translate.Request) (*translate.Result, error)
}

// Initializer is implemented by services that can prepare defaults themselves.
type Initializer interface {
	EnsureDefaults(ctx context.Context) error
	HasPacks() bool
}

// State is everything the window shows.
type State struct {
	mu     sync.Mutex
	svc    Translator
	ready  bool
	redraw func(func())

	langs   []string
	display map[string]string

	Src  string
	Tgt  string
	In   string
	Out  string
	Busy bool
	Note string
	ms   float64
}

// New builds a state backed by a real translation service.
func New() *State {
	s := &State{Src: "zh", Tgt: "en", display: map[string]string{"auto": "自动检测"}}
	s.loadLanguages()
	return s
}

// NewWithService builds a state for tests or for an already started service.
func NewWithService(svc Translator) *State {
	s := New()
	s.svc = svc
	s.ready = svc != nil
	return s
}

func (s *State) loadLanguages() {
	cat, err := pack.LoadCatalog()
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, p := range cat.Packs {
		for _, code := range []string{p.Source, p.Target} {
			if seen[code] {
				continue
			}
			seen[code] = true
			s.langs = append(s.langs, code)
			s.display[code] = fmt.Sprintf("%s (%s)", lang.Name(code), code)
		}
	}
	sort.Strings(s.langs)
}

// BindRedraw lets the window drive redraws from background work.
func (s *State) BindRedraw(fn func(func())) {
	s.mu.Lock()
	s.redraw = fn
	s.mu.Unlock()
}

func (s *State) post(fn func()) {
	s.mu.Lock()
	redraw := s.redraw
	s.mu.Unlock()
	if redraw != nil {
		redraw(fn)
	} else {
		fn()
	}
}

// Options returns the language choices shown in the selects.
func (s *State) Options() []string {
	out := []string{s.display["auto"]}
	for _, c := range s.langs {
		out = append(out, s.display[c])
	}
	return out
}

// Note returns the status line.
func (s *State) NoteText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Note
}

// SetNote replaces the status line.
func (s *State) SetNote(msg string) {
	s.mu.Lock()
	s.Note = msg
	s.mu.Unlock()
	s.post(func() {})
}

// Start prepares the runtime and the default packs in the background.
func (s *State) Start() {
	go func() {
		if !rtsetup.Installed() {
			s.SetNote("首次启动：下载 ONNX Runtime " + rtsetup.Version + " …")
			if _, err := rtsetup.Ensure(context.Background(), nil); err != nil {
				s.SetNote("运行库安装失败: " + err.Error())
				return
			}
		}
		cfg := llm.LoadConfig()
		svc, err := translate.New(translate.Options{
			AutoInstall: true,
			Engine:      "auto",
			LLM:         &cfg,
			Progress: func(packID, file string, done, total int64) {
				if total > 0 {
					s.SetNote(fmt.Sprintf("下载语言包 %s %d%%", packID, done*100/total))
				}
			},
		})
		if err != nil {
			s.SetNote("初始化失败: " + err.Error())
			return
		}
		s.mu.Lock()
		s.svc = svc
		s.ready = true
		s.mu.Unlock()
		if svc.HasPacks() {
			s.SetNote("就绪")
			return
		}
		s.SetNote("首次使用：准备默认语言包（中文 ↔ 英语）…")
		if err := svc.EnsureDefaults(context.Background()); err != nil {
			s.SetNote(err.Error())
			return
		}
		s.SetNote("就绪")
	}()
}

// Translate runs a request in the background.
func (s *State) Translate(srcText, srcLang, tgtLang string) {
	s.mu.Lock()
	svc := s.svc
	s.Busy = true
	s.Note = "翻译中…"
	s.mu.Unlock()
	s.post(func() {})
	if svc == nil {
		s.SetNote("引擎尚未就绪")
		s.mu.Lock()
		s.Busy = false
		s.mu.Unlock()
		return
	}
	go func() {
		res, err := svc.Translate(context.Background(), translate.Request{Text: srcText, Source: srcLang, Target: tgtLang})
		s.mu.Lock()
		s.Busy = false
		if err != nil {
			s.Note = err.Error()
		} else {
			s.Out = res.Text
			s.ms = res.Millis
			s.Note = fmt.Sprintf("%s → %s · %s · %.0fms", res.Source, res.Target, res.Pack, res.Millis)
		}
		s.mu.Unlock()
		s.post(func() {})
	}()
}

// CodeOf extracts the language code from a display label like "中文 (zh)".
func CodeOf(display string) string {
	if i := strings.LastIndex(display, "("); i >= 0 {
		return strings.TrimSuffix(display[i+1:], ")")
	}
	return display
}

// View renders the window. It is a pure function of the state, so the same
// view can be driven by MyGo's headless tester.
func (s *State) View(c *ui.Context) {
	t := c.Theme()

	s.mu.Lock()
	src, tgt := s.Src, s.Tgt
	in, out := s.In, s.Out
	busy := s.Busy
	note := s.Note
	s.mu.Unlock()

	srcDisplay := s.display[src]
	tgtDisplay := s.display[tgt]

	ui.Column(c).Fill().Padding(18).Gap(12).Children(func() {
		ui.Text(c, "translator233").FontSize(20).Bold().TextColor(t.Text)
		ui.Text(c, "本地离线神经网络翻译 · 无需联网 · ONNX Runtime").FontSize(12).TextColor(t.TextMuted)

		ui.Row(c).Gap(10).Children(func() {
			ui.Select(c, &srcDisplay, s.Options()).Width(190).Label("源语言")
			ui.Text(c, "→").FontSize(16).TextColor(t.TextMuted)
			ui.Select(c, &tgtDisplay, s.Options()).Width(190).Label("目标语言")
			ui.Spacer(c)
			if ui.Button(c, "交换").Label("交换语言").Disabled(busy).Clicked() {
				a, b := src, tgt
				if a != "auto" && b != "auto" {
					s.mu.Lock()
					s.Src, s.Tgt = b, a
					s.mu.Unlock()
				}
			}
		})
		if next := CodeOf(srcDisplay); next != src {
			s.mu.Lock()
			s.Src = next
			s.mu.Unlock()
		}
		if next := CodeOf(tgtDisplay); next != tgt {
			s.mu.Lock()
			s.Tgt = next
			s.mu.Unlock()
		}

		ui.Row(c).Fill().AlignItems(ui.Stretch).Gap(12).Children(func() {
			ui.Column(c).FillWidth().Grow(1).Gap(6).Children(func() {
				ui.Text(c, "原文").FontSize(12).TextColor(t.TextMuted)
				ui.TextArea(c, &in).FillHeight().Label("原文输入").Placeholder("输入要翻译的文本…")
			})
			ui.Column(c).FillWidth().Grow(1).Gap(6).Children(func() {
				ui.Text(c, "译文").FontSize(12).TextColor(t.TextMuted)
				ui.TextArea(c, &out).FillHeight().Label("译文输出").Placeholder("翻译结果")
			})
		})
		if in != s.In {
			s.mu.Lock()
			s.In = in
			s.mu.Unlock()
		}
		if out != s.Out {
			s.mu.Lock()
			s.Out = out
			s.mu.Unlock()
		}

		ui.Row(c).Gap(10).Children(func() {
			if ui.PrimaryButton(c, "翻译").Disabled(busy || strings.TrimSpace(in) == "").Clicked() {
				s.Translate(in, src, tgt)
			}
			if ui.Button(c, "清空").Clicked() {
				s.mu.Lock()
				s.In, s.Out = "", ""
				s.mu.Unlock()
			}
			if ui.Button(c, "复制译文").Clicked() {
				c.WriteClipboard(out)
			}
			ui.Spacer(c)
			ui.Text(c, note).FontSize(12).TextColor(t.TextMuted)
		})
	})
}
