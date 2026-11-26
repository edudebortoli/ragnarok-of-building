//go:build js

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"ragnarok-of-building/internal/ui"
	"strings"
	"sync/atomic"
	"syscall/js"
	"time"
)

// maxOpenFile caps what the file picker reads (the replay limit is 16 MB;
// one byte more lets the window say it is too big).
const maxOpenFile = 16<<20 + 1

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// pageURL resolves a path next to the page (the site may live in a
// subfolder, as GitHub Pages serves it).
func pageURL(rel string) string {
	return js.Global().Get("URL").New(rel, js.Global().Get("location").Get("href")).Call("toString").String()
}

// fetch downloads url, reporting the bytes read so far.
func fetch(url string, progress func(n int64)) ([]byte, error) {
	res, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, res.Status)
	}
	var buf bytes.Buffer
	chunk := make([]byte, 256<<10)
	for {
		n, err := res.Body.Read(chunk)
		buf.Write(chunk[:n])
		progress(int64(buf.Len()))
		if err == io.EOF {
			return buf.Bytes(), nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// openFile shows the browser's file picker and reads the chosen file. The
// picker opens only shortly after a click, which is when the window asks
// for it.
//
// It must always return, or the window keeps thinking a picker is open:
// a closed picker sends "cancel", and where the browser does not (Safari
// before 16.4), the page getting focus back with no file chosen a moment
// later counts as a cancel too.
func openFile(title, filter string) (string, []byte, error) {
	type result struct {
		name string
		data []byte
		err  error
	}
	done := make(chan result, 1)
	// JS callbacks must never block: only the first answer counts.
	send := func(r result) {
		select {
		case done <- r:
		default:
		}
	}
	doc := js.Global().Get("document")
	input := doc.Call("createElement", "input")
	input.Set("type", "file")
	input.Set("accept", acceptOf(filter))
	input.Get("style").Set("display", "none")
	doc.Get("body").Call("appendChild", input)

	var onChange, onCancel, onRead, onFail, onFocus js.Func
	release := func() {
		input.Call("removeEventListener", "change", onChange)
		input.Call("removeEventListener", "cancel", onCancel)
		js.Global().Call("removeEventListener", "focus", onFocus)
		input.Call("remove")
		onChange.Release()
		onCancel.Release()
		onRead.Release()
		onFail.Release()
		onFocus.Release()
	}
	var started atomic.Bool // a file was chosen and is being read
	focused := make(chan struct{}, 1)
	var name string
	onRead = js.FuncOf(func(_ js.Value, args []js.Value) any {
		arr := js.Global().Get("Uint8Array").New(args[0])
		b := make([]byte, arr.Get("length").Int())
		js.CopyBytesToGo(b, arr)
		send(result{name: name, data: b})
		return nil
	})
	onFail = js.FuncOf(func(_ js.Value, args []js.Value) any {
		send(result{err: errors.New("não deu para ler o arquivo")})
		return nil
	})
	onChange = js.FuncOf(func(js.Value, []js.Value) any {
		files := input.Get("files")
		if files.Get("length").Int() == 0 {
			send(result{err: ui.ErrNoFile})
			return nil
		}
		started.Store(true)
		f := files.Index(0)
		name = f.Get("name").String()
		f.Call("slice", 0, maxOpenFile).Call("arrayBuffer").Call("then", onRead, onFail)
		return nil
	})
	onCancel = js.FuncOf(func(js.Value, []js.Value) any {
		send(result{err: ui.ErrNoFile})
		return nil
	})
	onFocus = js.FuncOf(func(js.Value, []js.Value) any {
		select {
		case focused <- struct{}{}:
		default:
		}
		return nil
	})
	input.Call("addEventListener", "change", onChange)
	input.Call("addEventListener", "cancel", onCancel)
	js.Global().Call("addEventListener", "focus", onFocus)
	input.Call("click")
	for {
		select {
		case r := <-done:
			release()
			return r.name, r.data, r.err
		case <-focused:
			// "change" may come just after the focus: give it a moment.
			select {
			case r := <-done:
				release()
				return r.name, r.data, r.err
			case <-time.After(time.Second):
			}
			if !started.Load() {
				release()
				return "", nil, ui.ErrNoFile
			}
		}
	}
}

// acceptOf turns "Replays (*.rrf)|*.rrf" into ".rrf" for <input accept>.
func acceptOf(filter string) string {
	parts := strings.Split(filter, "|")
	var exts []string
	for i := 1; i < len(parts); i += 2 {
		for _, p := range strings.Split(parts[i], ";") {
			if ext, ok := strings.CutPrefix(strings.TrimSpace(p), "*"); ok && ext != ".*" {
				exts = append(exts, ext)
			}
		}
	}
	return strings.Join(exts, ",")
}

// saveFile hands the file to the browser as a download.
func saveFile(name string, data []byte) (string, error) {
	arr := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(arr, data)
	blob := js.Global().Get("Blob").New([]any{arr}, map[string]any{"type": "application/octet-stream"})
	url := js.Global().Get("URL").Call("createObjectURL", blob)
	a := js.Global().Get("document").Call("createElement", "a")
	a.Set("href", url)
	a.Set("download", name)
	a.Call("click")
	// Give the download a moment to start before freeing the blob.
	var revoke js.Func
	revoke = js.FuncOf(func(js.Value, []js.Value) any {
		js.Global().Get("URL").Call("revokeObjectURL", url)
		revoke.Release()
		return nil
	})
	js.Global().Call("setTimeout", revoke, 10000)
	return name + " (downloads)", nil
}
