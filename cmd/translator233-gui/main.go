// Command translator233-gui is the desktop window for translator233, drawn with
// MyGo's native UI toolkit (no webview, no JavaScript, no cgo).
package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/translator233/internal/gui"
)

func windowOptions() mygo.WindowOptions {
	return mygo.WindowOptions{
		Title:     "translator233 — 本地离线翻译",
		Width:     980,
		Height:    700,
		MinWidth:  640,
		MinHeight: 480,
	}
}

func main() {
	state := gui.New()
	mygo.App.WhenReady(func() {
		opts := windowOptions()
		opts.Content = ui.View(state.View)
		win := mygo.NewWindow(opts)
		state.BindRedraw(func(fn func()) { win.Update(fn) })
		state.Start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
