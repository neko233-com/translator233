package translate

import (
	"strings"
	"testing"
	"time"
)

func TestSplitKeepsSentences(t *testing.T) {
	text := "第一句。第二句！第三句？"
	segs := Split(text, 380)
	if len(segs) != 1 {
		t.Fatalf("short text should stay in one segment, got %d", len(segs))
	}
	if segs[0] != text {
		t.Fatalf("segment = %q", segs[0])
	}
}

func TestSplitRespectsMax(t *testing.T) {
	text := strings.Repeat("这是一句用于测试分句的文本。", 40)
	segs := Split(text, 100)
	if len(segs) < 2 {
		t.Fatalf("expected multiple segments, got %d", len(segs))
	}
	for _, s := range segs {
		if len([]rune(s)) > 100 {
			t.Fatalf("segment too long: %d runes", len([]rune(s)))
		}
	}
	joined := strings.Join(segs, "")
	if joined != text {
		t.Fatalf("segments do not cover the input")
	}
}

func TestSplitParagraphs(t *testing.T) {
	segs := Split("first paragraph\nsecond paragraph", 380)
	if len(segs) != 2 {
		t.Fatalf("lines should be separate segments, got %d", len(segs))
	}
}

func TestJoinSegments(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"Hello.", "World."}, "Hello. World."},
		{[]string{"Hello", "world"}, "Hello world"},
		{[]string{"你好。", "世界。"}, "你好。世界。"},
	}
	for _, c := range cases {
		if got := joinSegments(c.in); got != c.want {
			t.Errorf("joinSegments(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestShouldUnload(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		lastUsed time.Time
		busy     int
		idle     time.Duration
		want     bool
	}{
		{"idle engine", now.Add(-10 * time.Minute), 0, 5 * time.Minute, true},
		{"busy engine", now.Add(-10 * time.Minute), 1, 5 * time.Minute, false},
		{"recently used", now.Add(-time.Minute), 0, 5 * time.Minute, false},
		{"unloading disabled", now.Add(-time.Hour), 0, 0, false},
	}
	for _, c := range cases {
		if got := shouldUnload(now, c.lastUsed, c.busy, c.idle); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
