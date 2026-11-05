// Command translator233-gui is the desktop window for translator233, drawn with
// MyGo's native UI toolkit (no webview, no JavaScript).
package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/translator233/internal/lang"
	"github.com/neko233-com/translator233/internal/pack"
	"github.com/neko233-com/translator233/internal/rtsetup"
	"github.com/neko233-com/translator233/internal/translate"
)

type state struct {
	mu      sync.Mutex
	svc     *translate.Service
	win     *mygo.Window
	langs   []string
	display map[string]string

	src  string
	tgt  string
	in   string
	out  string
	busy bool
	note string
	ms   float64
}

func main() {
	s := newState()
	mygo.App.WhenReady(func() {
		s.win = mygo.NewWindow(mygo.WindowOptions{
			Title:  "translator233 — 本地离线翻译",
			Width:  900,
			Height: 620,
			Content: ui.View(s.view),
		})
		go s.init()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func newState() *state {
	s := &state{
		src:     "auto",
		tgt:     "en",
		display: map[string]string{"auto": "自动检测"},
	}
	cat, err := pack.LoadCatalog()
	if err != nil {
		return s
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
	if len(s.langs) > 0 {
		for _, c := range s.langs {
			if c == "zh" && s.src == "auto" {
				s.src = "zh"
				break
			}
		}
	}
	return s
}

func (s *state) options() []string {
	out := []string{s.display["auto"]}
	for _, c := range s.langs {
		out = append(out, s.display[c])
	}
	return out
}

func codeOf(display string) string {
	if i := strings.LastIndex(display, "("); i >= 0 {
		return strings.TrimSuffix(display[i+1:], ")")
	}
	return display
}

func (s *state) init() {
	if !rtsetup.Installed() {
		s.setNote("首次启动：下载 ONNX Runtime " + rtsetup.Version + " …")
		if _, err := rtsetup.Ensure(context.Background(), nil); err != nil {
			s.setNote("运行库安装失败: " + err.Error())
			return
		}
	}
	svc, err := translate.New(translate.Options{})
	if err != nil {
		s.setNote("初始化失败: " + err.Error())
		return
	}
	s.mu.Lock()
	s.svc = svc
	s.mu.Unlock()
	s.refresh("就绪")
}

func (s *state) setNote(msg string) {
	s.mu.Lock()
	s.note = msg
	s.mu.Unlock()
	if s.win != nil {
		s.win.Update(func() {})
	}
}

func (s *state) refresh(msg string) {
	s.mu.Lock()
	s.note = msg
	s.busy = false
	s.mu.Unlock()
	if s.win != nil {
		s.win.Update(func() {})
	}
}

func (s *state) translateAsync(srcText, srcLang, tgtLang string) {
	s.mu.Lock()
	svc := s.svc
	s.busy = true
	s.note = "翻译中…"
	s.mu.Unlock()
	if s.win != nil {
		s.win.Update(func() {})
	}
	go func() {
		res, err := svc.Translate(context.Background(), translate.Request{Text: srcText, Source: srcLang, Target: tgtLang})
		s.mu.Lock()
		s.busy = false
		if err != nil {
			s.note = err.Error()
		} else {
			s.out = res.Text
			s.ms = res.Millis
			s.note = fmt.Sprintf("%s → %s · %s · %.0fms", res.Source, res.Target, res.Pack, res.Millis)
		}
		s.mu.Unlock()
		if s.win != nil {
			s.win.Update(func() {})
		}
	}()
}

func (s *state) view(c *ui.Context) {
	t := c.Theme()

	srcDisplay := s.display[s.src]
	tgtDisplay := s.display[s.tgt]

	ui.Column(c).Fill().Padding(18).Gap(12).Children(func() {
		ui.Text(c, "translator233").FontSize(20).Bold().TextColor(t.Text)
		ui.Text(c, "本地离线神经网络翻译 · 无需联网 · ONNX Runtime").FontSize(12).TextColor(t.TextMuted)

		ui.Row(c).Gap(10).Children(func() {
			ui.Select(c, &srcDisplay, s.options()).Width(190)
			ui.Text(c, "→").FontSize(16).TextColor(t.TextMuted)
			ui.Select(c, &tgtDisplay, s.options()).Width(190)
			ui.Spacer(c)
			if ui.Button(c, "交换").Disabled(s.busy).Clicked() {
				a, b := s.src, s.tgt
				if a != "auto" && b != "auto" {
					s.src, s.tgt = b, a
				}
			}
		})
		s.src = codeOf(srcDisplay)
		s.tgt = codeOf(tgtDisplay)

		ui.Row(c).Fill().Gap(12).Children(func() {
			ui.Column(c).FillWidth().Grow(1).Gap(6).Children(func() {
				ui.Text(c, "原文").FontSize(12).TextColor(t.TextMuted)
				ui.TextArea(c, &s.in).FillHeight().Placeholder("输入要翻译的文本…")
			})
			ui.Column(c).FillWidth().Grow(1).Gap(6).Children(func() {
				ui.Text(c, "译文").FontSize(12).TextColor(t.TextMuted)
				ui.TextArea(c, &s.out).FillHeight().Placeholder("翻译结果")
			})
		})

		ui.Row(c).Gap(10).Children(func() {
			if ui.PrimaryButton(c, "翻译").Disabled(s.busy || strings.TrimSpace(s.in) == "").Clicked() {
				s.translateAsync(s.in, s.src, s.tgt)
			}
			if ui.Button(c, "清空").Clicked() {
				s.in, s.out = "", ""
			}
			if ui.Button(c, "复制译文").Clicked() {
				c.WriteClipboard(s.out)
			}
			ui.Spacer(c)
			ui.Text(c, s.note).FontSize(12).TextColor(t.TextMuted)
		})
	})
}
