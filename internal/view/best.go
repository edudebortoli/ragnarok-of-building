package view

import (
	"context"
	"maps"
	"runtime"
	"sort"
	"sync"
	"time"

	"ragnarok-of-building/internal/calc"
)

// Best item for a slot: every item that fits the slot and the class, each
// at the same refine and grade (items that can't take them get none), ranked
// by the expected damage against the damage window's target.

const (
	bestTop     = 15
	bestTimeout = 10 * time.Second
)

// BestItem is one ranked item.
type BestItem struct {
	Item  ItemView
	Gain  StatLine // expected damage change against the current build
	Ratio float64
	// Combo, for Melhor carta: the cards that complete one of the card's
	// sets, placed in the build too (the gain counts them).
	Combo []ComboPart
	// Traits, for Melhor carta: the gain is with the traits changed to these.
	Traits *[6]int
	// Opts, for Melhor item: the random options it was measured with.
	Opts     [][2]int
	OptsText string
}

// ComboPart is a card placed to complete a set: where and which.
type ComboPart struct {
	Slot      string
	Idx       int
	Card      int
	CardName  string
	SlotLabel string // "Elmo", "Armadura" ...
}

// BestResult is the ranking.
type BestResult struct {
	NoTarget bool
	Items    []BestItem
	Tested   int
	Partial  bool // stopped by the time limit
}

// swapAt is swapItem with the new item at refine and grade (Normalize
// drops what the item can't take).
func (s *Server) swapAt(b calc.Build, slot string, id, refine, grade int) calc.Build {
	nb := s.swapItem(b, slot, id, -1)
	for k, e := range nb.Equip {
		if e.Item == id && (k == slot || b.Equip[k].Item != id) {
			e.Refine, e.Grade = refine, grade
			nb.Equip[k] = e
		}
	}
	calc.Normalize(&nb, s.opt.Catalog)
	return nb
}

// BestItems ranks the items for slot (see above).
func (v *Views) BestItems(ctx context.Context, b calc.Build, slot string, refine, grade int, l string) BestResult {
	return v.BestItemsOpts(ctx, b, slot, refine, grade, false, l)
}

// BestItemsOpts is BestItems; with opts, each item gets its best random
// options (the items that rank highest without them go first).
func (v *Views) BestItemsOpts(ctx context.Context, b calc.Build, slot string, refine, grade int, opts bool, l string) BestResult {
	s := v.s
	c := s.opt.Catalog
	var res BestResult
	if c.Monster(b.Target) == nil {
		res.NoTarget = true
		return res
	}
	// A copy: Normalize writes to the maps, which are the caller's.
	b.Equip, b.Skills, b.Buffs = maps.Clone(b.Equip), maps.Clone(b.Skills), maps.Clone(b.Buffs)
	b.Atk.States = maps.Clone(b.Atk.States)
	if b.Equip == nil {
		b.Equip = map[string]calc.Equip{}
	}
	calc.Normalize(&b, c)
	if slot == calc.Shield && calc.ShieldBlocked(b, c) {
		return res
	}
	base := s.optExpected(b)
	if base <= 0 {
		res.NoTarget = true
		return res
	}
	job := c.Jobs[b.Job]
	var ids []int
	for _, it := range c.Items {
		if it.Type != "" && it.Type != "Card" && calc.Fits(it, slot) && job.CanEquip(it) && it.Name.Get(l) != "" {
			ids = append(ids, it.ID)
		}
	}
	sort.Ints(ids)
	ctx, cancel := context.WithTimeout(ctx, bestTimeout)
	defer cancel()
	type scored struct {
		id    int
		ratio float64
	}
	var (
		mu  sync.Mutex
		out []scored
		wg  sync.WaitGroup
	)
	ch := make(chan int)
	for range max(1, runtime.NumCPU()-1) {
		wg.Go(func() {
			for id := range ch {
				yield()
				// The item already worn counts as it is (its random options,
				// cards and enchants), not as a fresh copy: it reads "=".
				nb := b
				if id != b.Equip[slot].Item {
					nb = s.swapAt(b, slot, id, refine, grade)
				}
				e := s.optExpected(nb)
				mu.Lock()
				out = append(out, scored{id, e / base})
				mu.Unlock()
			}
		})
	}
feed:
	for _, id := range ids {
		select {
		case ch <- id:
		case <-ctx.Done():
			res.Partial = true
			break feed
		}
	}
	close(ch)
	wg.Wait()
	res.Tested = len(out)
	rank := func() {
		sort.Slice(out, func(i, j int) bool {
			if out[i].ratio != out[j].ratio {
				return out[i].ratio > out[j].ratio
			}
			return out[i].id < out[j].id
		})
	}
	rank()
	rolls := map[int][][2]int{}
	if opts {
		if !runPar(ctx, len(out), func(i int) {
			id := out[i].id
			if id == b.Equip[slot].Item || c.ItemOpts[id] == nil {
				return
			}
			nb := s.swapAt(b, slot, id, refine, grade)
			if o := s.bestOpts(nb, slot, GoalDamage); len(o) > 0 {
				e := s.optExpected(withOpts(nb, slot, o))
				mu.Lock()
				out[i].ratio, rolls[id] = e/base, o
				mu.Unlock()
			}
		}) {
			res.Partial = true
		}
		rank()
	}
	for _, sc := range out[:min(len(out), bestTop)] {
		ln := StatLine{Diff: pctChange(sc.ratio, l), Better: sc.ratio > 1.0005, Same: sc.ratio > 0.9995 && sc.ratio < 1.0005}
		if ln.Same {
			ln.Diff = "="
		}
		res.Items = append(res.Items, BestItem{Item: viewItem(c.Items[sc.id], l), Gain: ln, Ratio: sc.ratio,
			Opts: rolls[sc.id], OptsText: s.optsText(rolls[sc.id], l)})
	}
	return res
}

// SwapCompareAt is SwapCompare with the new item at refine and grade.
func (v *Views) SwapCompareAt(b calc.Build, slot string, id, refine, grade int, l string) SwapView {
	cur := ""
	if it := v.s.opt.Catalog.Items[b.Equip[slot].Item]; it != nil {
		cur = it.Name.Get(l)
	}
	return v.s.swapCompare(b, v.s.swapAt(b, slot, id, refine, grade), cur, l)
}

// SwapAt puts item id in the slot at refine and grade.
func (v *Views) SwapAt(b calc.Build, slot string, id, refine, grade int) calc.Build {
	return v.s.swapAt(b, slot, id, refine, grade)
}
