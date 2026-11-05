package gui

import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestZZDumpDark(t *testing.T) {
	path := os.Getenv("DUMP_DARK")
	if path == "" {
		t.Skip("DUMP_DARK not set")
	}
	s, _ := newTestState(t)
	tester := ui.NewTester(s.View, 980, 700)
	tester.SetDark(true)
	tester.Frame()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tester.Image()); err != nil {
		t.Fatal(err)
	}
}
