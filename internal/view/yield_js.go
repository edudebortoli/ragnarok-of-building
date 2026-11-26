//go:build js

package view

import (
	"sync"
	"sync/atomic"
	"syscall/js"
	"time"
)

// In the browser Go runs on one thread without preemption: a search that
// never blocks keeps the page from drawing until it ends, and the page
// draws only when every goroutine waits. yield, called once per candidate,
// waits for the next animation frame (or 50 ms when the tab is hidden and
// frames stop) once ~30 ms went by since the last frame. Every search
// running at that moment waits on the same frame, so they all stop
// together and the page gets to draw.

const (
	yieldEvery = 30 * time.Millisecond
	yieldMax   = 50 * time.Millisecond
)

var (
	lastFrame atomic.Int64 // unix nanos when the last frame let the searches go

	frameMu   sync.Mutex
	nextFrame chan struct{} // closed by the next frame; nil when none is asked for

	// onFrame is never released: a frame asked for may come after the
	// timeout.
	onFrame = js.FuncOf(func(js.Value, []js.Value) any {
		frameMu.Lock()
		if nextFrame != nil {
			close(nextFrame)
			nextFrame = nil
		}
		frameMu.Unlock()
		lastFrame.Store(time.Now().UnixNano())
		return nil
	})
)

func yield() {
	if time.Now().UnixNano()-lastFrame.Load() < int64(yieldEvery) {
		return
	}
	frameMu.Lock()
	if nextFrame == nil {
		nextFrame = make(chan struct{})
		js.Global().Call("requestAnimationFrame", onFrame)
	}
	wait := nextFrame
	frameMu.Unlock()
	select {
	case <-wait:
	case <-time.After(yieldMax):
		lastFrame.Store(time.Now().UnixNano()) // hidden tab: go on, try again later
	}
}
