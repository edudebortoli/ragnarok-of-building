package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"

	"gioui.org/app"
	"gioui.org/op"
	"gioui.org/unit"

	"ragnarok-of-building/internal/store"
	"ragnarok-of-building/internal/ui"
	"ragnarok-of-building/internal/view"
)

// The program's window (Gio): the startup screen while the game data gets
// ready, then the simulator drawn by internal/ui. No browser engine.

func init() { runNative = nativeMain }

// nativeMain never returns: Gio's app.Main keeps the main thread.
func nativeMain(st *startup, o options, log *slog.Logger) {
	files := store.Dir(".") // data/ui.json and builds.json, next to the program
	ui.UseSavedTheme(files) // before the startup screen
	th := ui.NewTheme()
	w := new(app.Window)
	w.Option(app.Title(appTitle), app.Size(unit.Dp(1440), unit.Dp(900)), app.MinSize(unit.Dp(640), unit.Dp(480)))

	var simulator atomic.Pointer[ui.App]
	start := &ui.Startup{Th: th, Pick: pickFolder, UseClient: st.useClient, Status: func() ui.StartupStatus {
		s := st.status()
		return ui.StartupStatus{Lines: s.Lines, Ask: s.Ask, AskNote: s.AskNote, Error: s.Error}
	}}
	st.onChange = w.Invalidate
	start.Invalidate = w.Invalidate

	go func() {
		v, err := prepare(st, o, log)
		if err != nil {
			st.fail(err)
			return
		}
		a := ui.NewApp(v, th, files)
		a.Icons = ui.NewIcons(view.NewIconCache(filepath.Join("data", "cache", "img")).Get, w.Invalidate)
		a.Invalidate = w.Invalidate
		a.OpenFile = openFile
		a.SaveFile = saveFile
		a.OnTheme = func(on bool) { go darkTitleBar(on) }
		simulator.Store(a)
		w.Invalidate()
	}()

	go func() {
		var ops op.Ops
		titled := false
		for {
			switch e := w.Event().(type) {
			case app.DestroyEvent:
				if e.Err != nil {
					log.Error("window", "err", e.Err)
					fatal("a janela do programa não abriu: " + e.Err.Error())
					os.Exit(1)
				}
				if a := simulator.Load(); a != nil {
					a.SaveNow()
				}
				os.Exit(0)
			case app.FrameEvent:
				if !titled {
					titled = true
					go darkTitleBar(ui.IsDark())
				}
				gtx := app.NewContext(&ops, e)
				if a := simulator.Load(); a != nil {
					a.Frame(gtx)
				} else {
					start.Frame(gtx)
				}
				e.Frame(gtx.Ops)
			}
		}
	}()
	app.Main()
}
