package ui

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"sync"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"ragnarok-of-building/internal/view"
)

// Icons loads item/skill/monster/class icons in the background (from the
// icon cache, which downloads each one once) and keeps them in memory.
type Icons struct {
	Get        func(ctx context.Context, rel string) ([]byte, error) // a PNG (view.IconCache.Get)
	Invalidate func()                                                // redraw when one arrives

	mu      sync.Mutex
	imgs    map[string]paint.ImageOp
	arts    map[string]*image.NRGBA // cut art (nil = none)
	fitted  map[fitKey]paint.ImageOp
	pending map[string]bool
	sem     chan struct{}
}

// NewIcons uses get to fetch and invalidate to ask for a redraw.
func NewIcons(get func(ctx context.Context, rel string) ([]byte, error), invalidate func()) *Icons {
	return &Icons{Get: get, Invalidate: invalidate, imgs: map[string]paint.ImageOp{}, arts: map[string]*image.NRGBA{}, fitted: map[fitKey]paint.ImageOp{}, pending: map[string]bool{}, sem: make(chan struct{}, 6)}
}

// fitKey is an art at a size in pixels.
type fitKey struct {
	rel string
	px  int
}

// Img returns the icon, loading it when needed (ok false until it arrives
// or when there is none).
func (ic *Icons) Img(rel string) (paint.ImageOp, bool) {
	if ic == nil || rel == "" {
		return paint.ImageOp{}, false
	}
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if op, ok := ic.imgs[rel]; ok {
		return op, op.Size().X > 0
	}
	if !ic.pending[rel] {
		ic.pending[rel] = true
		go ic.load(rel)
	}
	return paint.ImageOp{}, false
}

func (ic *Icons) load(rel string) {
	ic.sem <- struct{}{}
	defer func() { <-ic.sem }()
	var op paint.ImageOp
	if b, err := ic.Get(context.Background(), rel); err == nil {
		if img, err := png.Decode(bytes.NewReader(b)); err == nil {
			op = paint.NewImageOp(img)
			op.Filter = paint.FilterNearest // pixel art
		}
	}
	ic.mu.Lock()
	ic.imgs[rel] = op
	delete(ic.pending, rel)
	ic.mu.Unlock()
	if ic.Invalidate != nil {
		ic.Invalidate()
	}
}

// Art lays an item's big picture, scaled down, in a size×size dp box. The
// 24 px icon stands in while it loads and for items without one.
func (ic *Icons) Art(gtx C, item int, size unit.Dp) D {
	px := gtx.Dp(size)
	sz := image.Pt(px, px)
	img, ok := ic.art(view.ItemArt(item), px)
	if !ok {
		g := gtx
		g.Constraints = layout.Exact(sz)
		return layout.Center.Layout(g, func(gtx C) D { return ic.Icon(gtx, view.ItemIcon(item), min(size, 24)) })
	}
	st := op.Offset(sz.Sub(img.Size()).Div(2)).Push(gtx.Ops)
	img.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	st.Pop()
	return D{Size: sz}
}

func (ic *Icons) art(rel string, px int) (paint.ImageOp, bool) {
	if ic == nil {
		return paint.ImageOp{}, false
	}
	ic.mu.Lock()
	defer ic.mu.Unlock()
	k := fitKey{rel, px}
	if img, ok := ic.fitted[k]; ok {
		return img, true
	}
	src, ok := ic.arts[rel]
	if !ok && !ic.pending[rel] {
		ic.pending[rel] = true
		go ic.loadArt(rel)
	}
	if src == nil {
		return paint.ImageOp{}, false
	}
	img := paint.NewImageOp(fitArt(src, px))
	img.Filter = paint.FilterNearest // already at its size
	ic.fitted[k] = img
	return img, true
}

func (ic *Icons) loadArt(rel string) {
	ic.sem <- struct{}{}
	defer func() { <-ic.sem }()
	var art *image.NRGBA
	if b, err := ic.Get(context.Background(), rel); err == nil {
		if img, err := png.Decode(bytes.NewReader(b)); err == nil {
			art = cutArt(img)
			if s := art.Bounds().Size(); s.X < 8 || s.Y < 8 {
				art = nil // nothing left: the icon is better
			}
		}
	}
	ic.mu.Lock()
	ic.arts[rel] = art
	delete(ic.pending, rel)
	ic.mu.Unlock()
	if ic.Invalidate != nil {
		ic.Invalidate()
	}
}

// Icon lays an icon in a size×size dp box (empty while loading).
func (ic *Icons) Icon(gtx C, rel string, size unit.Dp) D {
	px := gtx.Dp(size)
	sz := image.Pt(px, px)
	img, ok := ic.Img(rel)
	if !ok {
		return D{Size: sz}
	}
	g := gtx
	g.Constraints.Min, g.Constraints.Max = sz, sz
	widget.Image{Src: img, Fit: widget.ScaleDown, Scale: 1}.Layout(g)
	return D{Size: sz}
}
