package gui

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/translator233/internal/translate"
)

type fakeService struct {
	calls []translate.Request
	out   string
}

func (f *fakeService) Translate(_ context.Context, req translate.Request) (*translate.Result, error) {
	f.calls = append(f.calls, req)
	return &translate.Result{Text: f.out, Source: req.Source, Target: req.Target, Pack: "zh-en", Engine: "local"}, nil
}

func newTestState(t *testing.T) (*State, *fakeService) {
	t.Helper()
	fake := &fakeService{out: "Hello, world."}
	s := NewWithService(fake)
	s.Src, s.Tgt = "zh", "en"
	return s, fake
}

func TestViewRendersCoreControls(t *testing.T) {
	s, _ := newTestState(t)
	tester := ui.NewTester(s.View, 900, 620)

	for _, want := range []string{"原文", "译文", "翻译", "清空", "复制译文", "交换"} {
		if !tester.HasText(want) {
			t.Errorf("window is missing %q; texts=%v", want, tester.Texts())
		}
	}
}

// TestViewStretchesWithWindow is the responsive-layout contract: growing the
// window must grow both panes, keeping them side by side and equal.
func TestViewStretchesWithWindow(t *testing.T) {
	s, _ := newTestState(t)
	tester := ui.NewTester(s.View, 900, 620)

	small, ok := tester.Find("原文输入")
	if !ok {
		t.Fatal("source text area not found")
	}
	smallTgt, ok := tester.Find("译文输出")
	if !ok {
		t.Fatal("target text area not found")
	}

	tester.SetSize(1400, 900)
	big, ok := tester.Find("原文输入")
	if !ok {
		t.Fatal("source text area disappeared after resize")
	}
	bigTgt, ok := tester.Find("译文输出")
	if !ok {
		t.Fatal("target text area disappeared after resize")
	}

	if big.W <= small.W {
		t.Errorf("source pane did not widen: %v -> %v", small.W, big.W)
	}
	if big.H <= small.H {
		t.Errorf("source pane did not grow in height: %v -> %v", small.H, big.H)
	}
	if big.W+bigTgt.W <= small.W+smallTgt.W {
		t.Errorf("panes did not grow together")
	}
	// Side by side, roughly the same width.
	if diff := big.W - bigTgt.W; diff > 4 || diff < -4 {
		t.Errorf("panes are unbalanced: %v vs %v", big.W, bigTgt.W)
	}
	if bigTgt.X <= big.X {
		t.Errorf("target pane is not to the right of the source pane")
	}
}

// TestViewFitsMinimumWindow makes sure the layout stays inside the smallest
// window the app allows.
func TestViewFitsMinimumWindow(t *testing.T) {
	s, _ := newTestState(t)
	tester := ui.NewTester(s.View, 520, 380)
	for _, label := range []string{"原文输入", "译文输出"} {
		r, ok := tester.Find(label)
		if !ok {
			t.Fatalf("%s not found", label)
		}
		if r.X < 0 || r.Y < 0 || r.X+r.W > 520+1 || r.Y+r.H > 380+1 {
			t.Errorf("%s overflows the minimum window: %+v", label, r)
		}
	}
}

func TestTranslateFillsOutput(t *testing.T) {
	s, fake := newTestState(t)
	s.In = "你好，世界！"
	s.Translate(s.In, "zh", "en")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		out, busy := s.Out, s.Busy
		s.mu.Unlock()
		if !busy && out != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	s.mu.Lock()
	out, note := s.Out, s.Note
	s.mu.Unlock()
	if out != "Hello, world." {
		t.Fatalf("out = %q, want %q", out, "Hello, world.")
	}
	if len(fake.calls) != 1 {
		t.Fatalf("service calls = %d", len(fake.calls))
	}
	if fake.calls[0].Source != "zh" || fake.calls[0].Target != "en" {
		t.Fatalf("unexpected request %+v", fake.calls[0])
	}
	if note == "" || note == "翻译中…" {
		t.Fatalf("status line not updated: %q", note)
	}
}

func TestTranslateWithoutServiceReportsReady(t *testing.T) {
	s := NewWithService(nil)
	s.Translate("hi", "en", "zh")
	if got := s.NoteText(); got != "引擎尚未就绪" {
		t.Fatalf("note = %q", got)
	}
}

func TestCodeOf(t *testing.T) {
	cases := map[string]string{
		"中文 (zh)":      "zh",
		"自动检测":         "自动检测",
		"English (en)": "en",
	}
	for in, want := range cases {
		if got := CodeOf(in); got != want {
			t.Errorf("CodeOf(%q) = %q, want %q", in, got, want)
		}
	}
}
