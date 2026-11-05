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

	"github.com/egoist/mygo/plugins/glass"
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

// label renders a language code for the pane headers.
func (s *State) label(code string) string {
	if code == "" || code == "auto" {
		return "自动检测"
	}
	return lang.Name(code)
}

// CodeOf extracts the language code from a display label like "中文 (zh)".
func CodeOf(display string) string {
	if i := strings.LastIndex(display, "("); i >= 0 {
		return strings.TrimSuffix(display[i+1:], ")")
	}
	return display
}

// palette is the set of colors the window paints with, in light or dark.
type palette struct {
	bgFrom, bgTo ui.Color
	glowA, glowB ui.Color
	cardBorder   ui.Color
	glassTint    ui.Color
	inset        ui.Color
	text, muted  ui.Color
	accent       ui.Color
	accentText   ui.Color
}

func paletteOf(t *ui.Theme) palette {
	if t.Dark {
		return palette{
			bgFrom:     ui.Hex("#0A0C11"),
			bgTo:       ui.Hex("#171B25"),
			glowA:      ui.RGBA(88, 120, 255, 0.55),
			glowB:      ui.RGBA(255, 96, 180, 0.42),
			cardBorder: ui.RGBA(255, 255, 255, 0.14),
			glassTint:  ui.RGBA(255, 255, 255, 0.06),
			inset:      ui.RGBA(255, 255, 255, 0.06),
			text:       ui.Hex("#F2F4F8"),
			muted:      ui.Hex("#8E96A6"),
			accent:     ui.Hex("#4C8DFF"),
			accentText: ui.Hex("#FFFFFF"),
		}
	}
	return palette{
		bgFrom:     ui.Hex("#EDF1FA"),
		bgTo:       ui.Hex("#D9E1F0"),
		glowA:      ui.RGBA(76, 141, 255, 0.55),
		glowB:      ui.RGBA(255, 118, 186, 0.45),
		cardBorder: ui.RGBA(255, 255, 255, 0.85),
		inset:      ui.RGBA(255, 255, 255, 0.60),
		text:       ui.Hex("#131722"),
		muted:      ui.Hex("#6B7280"),
		accent:     ui.Hex("#2F6FEB"),
		accentText: ui.Hex("#FFFFFF"),
	}
}

// View renders the window. It is a pure function of the state, so the same
// view can be driven by MyGo's headless tester.
func (s *State) View(c *ui.Context) {
	theme := c.Theme()
	p := paletteOf(theme)

	s.mu.Lock()
	src, tgt := s.Src, s.Tgt
	in, out := s.In, s.Out
	busy := s.Busy
	note := s.Note
	ms := s.ms
	s.mu.Unlock()

	srcDisplay := s.display[src]
	tgtDisplay := s.display[tgt]

	ui.Column(c).Fill().Gradient(p.bgFrom, p.bgTo, 165).Children(func() {
		// Ambient colour wash behind the glass, like a blurred wallpaper.
		s.glow(c, -140, -180, 560, p.glowA)
		s.glow(c, 720, 300, 620, p.glowB)

		ui.Column(c).Fill().Padding(24, 28).Gap(16).Children(func() {
			s.header(c, p, note)

			// The main glass card.
			ui.Column(c).Fill().Radius(26).Padding(20).Gap(14).
				Border(1, p.cardBorder).
				Shadow(0, 18, 44, -18, ui.RGBA(20, 30, 60, 0.28)).
				Material(glass.Glass{Style: glass.Regular, Tint: p.glassTint, Clarity: 0}).
				Children(func() {
					s.toolbar(c, p, &srcDisplay, &tgtDisplay, busy, in)
					s.panes(c, p, &in, &out, ms)
				})

			ui.Row(c).Gap(10).Padding(0, 4).Children(func() {
				ui.Text(c, "Ctrl + Enter 翻译 · 本地离线 · 文本不出本机").FontSize(11).TextColor(p.muted)
			})
		})
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
}

// glow paints one soft ambient blob that the glass above it frosts.
func (s *State) glow(c *ui.Context, left, top, size float32, color ui.Color) {
	ui.Box(c).Absolute().Left(left).Top(top).Width(size).Height(size).
		RadialGradient(ui.RadialGradient{
			Stops:  []ui.GradientStop{{Color: color, At: 0}, {Color: ui.Transparent, At: 1}},
			Radius: size / 2,
		})
}

func (s *State) header(c *ui.Context, p palette, note string) {
	ui.Row(c).Gap(12).Children(func() {
		ui.Box(c).Width(38).Height(38).Radius(12).
			Gradient(ui.Hex("#6FA8FF"), ui.Hex("#2F6FEB"), 145).
			Shadow(0, 6, 14, -6, ui.RGBA(47, 111, 235, 0.55))
		ui.Column(c).Gap(1).Children(func() {
			ui.Text(c, "translator233").FontSize(19).Bold().TextColor(p.text)
			ui.Text(c, "本地离线神经网络翻译").FontSize(11).TextColor(p.muted)
		})
		ui.Spacer(c)
		ui.Row(c).Gap(8).Padding(8, 14).Radius(14).
			Background(p.inset).Border(1, p.cardBorder).
			Children(func() {
				ui.Text(c, note).FontSize(11).TextColor(p.muted)
			})
	})
}

func (s *State) toolbar(c *ui.Context, p palette, srcDisplay, tgtDisplay *string, busy bool, in string) {
	ui.Row(c).Gap(10).Children(func() {
		ui.Select(c, srcDisplay, s.Options()).Width(176).Label("源语言")
		if ui.Button(c, "⇄").Label("交换语言").Width(40).Radius(12).
			Disabled(busy).Clicked() {
			a, b := s.Src, s.Tgt
			if a != "auto" && b != "auto" {
				s.mu.Lock()
				s.Src, s.Tgt = b, a
				s.mu.Unlock()
			}
		}
		ui.Select(c, tgtDisplay, s.Options()).Width(176).Label("目标语言")
		ui.Spacer(c)
		if ui.Button(c, "清空").Radius(12).Disabled(busy).Clicked() {
			s.mu.Lock()
			s.In, s.Out = "", ""
			s.mu.Unlock()
		}
		if ui.Button(c, "复制").Label("复制译文").Radius(12).Disabled(busy).Clicked() {
			s.mu.Lock()
			text := s.Out
			s.mu.Unlock()
			c.WriteClipboard(text)
		}
		btn := ui.PrimaryButton(c, "翻译").Label("翻译").Radius(12).
			Disabled(busy || strings.TrimSpace(in) == "")
		if btn.Clicked() {
			s.Translate(in, s.Src, s.Tgt)
		}
	})
}

func (s *State) panes(c *ui.Context, p palette, in, out *string, ms float64) {
	ui.Row(c).Fill().AlignItems(ui.Stretch).Gap(14).Children(func() {
		ui.Column(c).FillWidth().Grow(1).Gap(8).Children(func() {
			ui.Row(c).Children(func() {
				ui.Text(c, "原文").FontSize(11).Bold().TextColor(p.muted)
				ui.Spacer(c)
			})
			ui.Column(c).Fill().Radius(16).Padding(4).
				Background(p.inset).Border(1, p.cardBorder).
				Children(func() {
					ui.TextArea(c, in).FillHeight().Label("原文输入").
						Placeholder("输入或粘贴要翻译的文本…")
				})
		})
		ui.Column(c).FillWidth().Grow(1).Gap(8).Children(func() {
			ui.Row(c).Children(func() {
				ui.Text(c, "译文").FontSize(11).Bold().TextColor(p.muted)
				ui.Spacer(c)
				if ms > 0 {
					ui.Text(c, fmt.Sprintf("%.0f ms", ms)).FontSize(10).TextColor(p.muted)
				}
			})
			ui.Column(c).Fill().Radius(16).Padding(4).
				Background(p.inset).Border(1, p.cardBorder).
				Children(func() {
					ui.TextArea(c, out).FillHeight().Label("译文输出").
						Placeholder("翻译结果")
				})
		})
	})
}
