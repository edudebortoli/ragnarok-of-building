//go:build js

// web is Ragnarok of Building in the browser: the same Gio window as the
// desktop program, compiled to WebAssembly and served as a static site
// (index.html, wasm_exec.js, app.wasm, catalog.json.gz; see `make web`).
//
// Differences from the desktop: the game data is the catalog served next
// to the page (no client import), the window state and saved builds live
// in this browser's localStorage, files are opened with the browser's
// picker and saved as downloads.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sync"
	"syscall/js"
	"time"

	"gioui.org/app"
	"gioui.org/op"
	"gioui.org/unit"

	"ragnarok-of-building/internal/gamedata"
	"ragnarok-of-building/internal/store"
	"ragnarok-of-building/internal/ui"
	"ragnarok-of-building/internal/view"
)

const appTitle = "Ragnarok of Building"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})) // the browser console
	files := store.Browser()
	ui.UseSavedTheme(files) // before the startup screen
	th := ui.NewTheme()
	w := new(app.Window)
	w.Option(app.Title(appTitle), app.MinSize(unit.Dp(640), unit.Dp(480)))

	st := &status{invalidate: w.Invalidate}
	start := &ui.Startup{Th: th, Status: st.get, Invalidate: w.Invalidate}
	var simulator *ui.App
	ready := make(chan *ui.App, 1)

	go func() {
		v, err := load(st, log)
		if err != nil {
			log.Error("startup", "err", err)
			st.fail(err)
			return
		}
		a := ui.NewApp(v, th, files)
		a.Invalidate = w.Invalidate
		a.OpenFile = openFile
		a.SaveFile = saveFile
		a.ShareURL = shareURL
		a.NoPaste = true
		saveOnHide(a)
		openLinkedBuild(a)
		ready <- a
		w.Invalidate()
	}()

	go func() {
		var ops op.Ops
		shown := false
		for {
			switch e := w.Event().(type) {
			case app.DestroyEvent:
				if simulator != nil {
					simulator.SaveNow()
				}
				return // the page stays; nothing to exit
			case app.FrameEvent:
				if !shown {
					shown = true
					hideLoading()
				}
				select {
				case simulator = <-ready:
				default:
				}
				gtx := app.NewContext(&ops, e)
				if simulator != nil {
					simulator.Frame(gtx)
				} else {
					start.Frame(gtx)
				}
				e.Frame(gtx.Ops)
			}
		}
	}()
	app.Main()
}

// load downloads the catalog served next to the page and prepares the
// views.
func load(st *status, log *slog.Logger) (*view.Views, error) {
	st.say("Baixando os dados do jogo…")
	b, err := fetch(pageURL("catalog.json.gz"), func(n int64) {
		st.replaceLast(fmt.Sprintf("Baixando os dados do jogo… %.1f MB", float64(n)/(1<<20)))
	})
	if err != nil {
		return nil, fmt.Errorf("não deu para baixar os dados do jogo: %w", err)
	}
	st.say("Preparando os dados do jogo…")
	// One thread: let the browser draw that line before decoding takes it.
	time.Sleep(100 * time.Millisecond)
	cat, err := gamedata.Read(bytesReader(b))
	if err != nil {
		return nil, fmt.Errorf("catálogo do jogo: %w", err)
	}
	return view.New(view.Options{Log: log, Catalog: cat})
}

// status is what the startup screen shows.
type status struct {
	mu         sync.Mutex
	lines      []string
	err        string
	invalidate func()
}

func (s *status) get() ui.StartupStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ui.StartupStatus{Lines: append([]string(nil), s.lines...), Error: s.err}
}

func (s *status) say(line string) {
	s.mu.Lock()
	s.lines = append(s.lines, line)
	s.mu.Unlock()
	s.invalidate()
}

func (s *status) replaceLast(line string) {
	s.mu.Lock()
	if n := len(s.lines); n > 0 {
		s.lines[n-1] = line
	}
	s.mu.Unlock()
	s.invalidate()
}

func (s *status) fail(err error) {
	s.mu.Lock()
	s.err = err.Error()
	s.mu.Unlock()
	s.invalidate()
}

// saveOnHide writes a pending state when the tab is hidden or closed: the
// state is written 400 ms after the last change, by a frame, and a hidden
// tab gets no frames (a closed one never will). Gio sends no DestroyEvent
// for a closing tab. Calling SaveNow from a JS event is safe here: the
// page has one thread and JS events run only while every goroutine waits,
// so no frame is halfway through.
func saveOnHide(a *ui.App) {
	doc := js.Global().Get("document")
	save := js.FuncOf(func(js.Value, []js.Value) any {
		if doc.Get("visibilityState").String() == "hidden" {
			a.SaveNow()
		}
		return nil
	})
	doc.Call("addEventListener", "visibilitychange", save) // for the page's lifetime
	js.Global().Call("addEventListener", "pagehide", js.FuncOf(func(js.Value, []js.Value) any {
		a.SaveNow()
		return nil
	}))
}

// shareURL is this page's address with the build after "#b=": the part
// after # never reaches a server and has no practical length limit.
func shareURL(code string) string { return pageBase() + "#b=" + code }

// pageBase is the page's address without query or fragment.
func pageBase() string {
	loc := js.Global().Get("location")
	return loc.Get("origin").String() + loc.Get("pathname").String()
}

var linkCode = regexp.MustCompile(`[?&#]b=([A-Za-z0-9_-]+)`)

// openLinkedBuild opens the build in the page's link (#b=..., or ?b= from
// older links), now and whenever the link changes while the page is open.
// The code then leaves the address bar, so a reload keeps the player's
// edits instead of reopening the linked build.
func openLinkedBuild(a *ui.App) {
	open := func() {
		m := linkCode.FindStringSubmatch(js.Global().Get("location").Get("href").String())
		if m == nil {
			return
		}
		a.OpenShared(m[1])
		js.Global().Get("history").Call("replaceState", nil, "", pageBase())
	}
	open()
	js.Global().Call("addEventListener", "hashchange", js.FuncOf(func(js.Value, []js.Value) any {
		open()
		return nil
	}))
}

// hideLoading removes index.html's loading message once the window draws.
func hideLoading() {
	if el := js.Global().Get("document").Call("getElementById", "loading"); el.Truthy() {
		el.Call("remove")
	}
}
