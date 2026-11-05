package lang

import "testing"

func TestDetect(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"你好，世界！", "zh"},
		{"こんにちは、世界", "ja"},
		{"안녕하세요", "ko"},
		{"Привет, мир", "ru"},
		{"مرحبا بالعالم", "ar"},
		{"สวัสดีชาวโลก", "th"},
		{"नमस्ते दुनिया", "hi"},
		{"The quick brown fox jumps over the lazy dog.", "en"},
		{"Le chat est sur la table et il dort.", "fr"},
		{"Der Hund ist nicht sehr groß und sie ist müde.", "de"},
		{"", ""},
		{"123 !!!", ""},
	}
	for _, c := range cases {
		if got := Detect(c.text); got != c.want {
			t.Errorf("Detect(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestName(t *testing.T) {
	if Name("zh") != "中文" {
		t.Fatalf("Name(zh) = %q", Name("zh"))
	}
	if Name("xx") != "xx" {
		t.Fatalf("unknown code should return itself")
	}
}
