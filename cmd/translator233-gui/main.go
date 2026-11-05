// Command translator233-gui is the desktop window for translator233, drawn with
// MyGo's native UI toolkit (no webview, no JavaScript, no cgo).
package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/neko233-com/translator233/internal/gui"
)

func setTitleBarStyle() mygo.WindowOptions {
	return mygo.WindowOptions{
		Title:     "translator233 — 本地离线翻译",
		Width:     900,
		Height:    620,
		MinWidth:  520,
		MinHeight: 380,
	}
}

func main() {
	state := gui.New()
	mygo.App.WhenReady(func() {
		opts := setTitleBarStyle()
		opts.Content = ui.View(state.View)
		win := mygo.NewWindow(opts)
		state.BindRedraw(func(fn func()) { win.Update(fn) })
		state.Start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
