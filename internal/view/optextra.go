package view

import (
	"context"
	"maps"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ragnarok-of-building/internal/calc"
	"ragnarok-of-building/internal/gamedata"
)

// The optimizer's extras: what a card or enchant gives at higher refines and
// grades of its item, random options, and the enchant stones of costumes
// with their sets.

// OptStep is what an option gives once its item reaches a refine or grade.
type OptStep struct {
	Refine, Grade int // the item's
	Gain          string
}

const (
	optStepMin   = 0.3 // percentage points a step must add to be listed
	optLaterMax  = 2   // options that only pay off at a higher refine or grade
	laterRefine  = 15  // where those are measured
	extraTimeout = 8 * time.Second
)

// stepRefines are the refines listed: the ones that unlock bonuses.
var stepRefines = []int{5, 7, 9, 10, 11, 12, 14, 15, 16, 18, 20}

var reScales = regexp.MustCompile(`getrefine|getenchantgrade|getequiprefinerycnt`)

// scalesWithItem reports a card or enchant that reads the refine or grade.
func scalesWithItem(it *gamedata.Item) bool {
	return it != nil && (reScales.MatchString(it.Script) || reScales.MatchString(it.DescScript))
}

// linkedSlots are the slot and the head slots holding the same item.
func linkedSlots(b calc.Build, slot string) []string {
	out := []string{slot}
	if strings.HasPrefix(slot, "Head_") && b.Equip[slot].Item != 0 {
		for _, h := range []string{calc.HeadTop, calc.HeadMid, calc.HeadLow} {
			if h != slot && b.Equip[h].Item == b.Equip[slot].Item {
				out = append(out, h)
			}
		}
	}
	return out
}

// editSlots is the build with fn applied to a copy of the slots' equipment.
func editSlots(b calc.Build, slots []string, fn func(e *calc.Equip)) calc.Build {
	b.Equip = maps.Clone(b.Equip)
	for _, k := range slots {
		e := b.Equip[k]
		e.Opts = slices.Clone(e.Opts)
		fn(&e)
		b.Equip[k] = e
	}
	return b
}

// runPar calls fn(i) for every i in parallel until ctx ends; false when it
// did not get through all.
func runPar(ctx context.Context, n int, fn func(i int)) bool {
	ch := make(chan int)
	var wg sync.WaitGroup
	for range max(1, runtime.NumCPU()-1) {
		wg.Go(func() {
			for i := range ch {
				yield()
				fn(i)
			}
		})
	}
	done := true
feed:
	for i := range n {
		select {
		case ch <- i:
		case <-ctx.Done():
			done = false
			break feed
		}
	}
	close(ch)
	wg.Wait()
	return done
}

// ---- Refine and grade ----

// optSteps measures card id at idx of the slots' item at higher refines and
// grades, each time against what is there now at that same refine and grade.
func (s *Server) optSteps(ctx context.Context, b calc.Build, slots []string, idx, id int, goal, l string) []OptStep {
	if !scalesWithItem(s.opt.Catalog.Items[id]) {
		return nil
	}
	return s.stepsOf(ctx, b, slots, func(e *calc.Equip) { e.Cards[idx] = id }, goal, l)
}

// stepsOf is optSteps for any change to the slots' equipment.
func (s *Server) stepsOf(ctx context.Context, b calc.Build, slots []string, change func(e *calc.Equip), goal, l string) []OptStep {
	c := s.opt.Catalog
	e := b.Equip[slots[0]]
	host := c.Items[e.Item]
	if host == nil {
		return nil
	}
	gain := func(refine, grade int) float64 {
		at := editSlots(b, slots, func(e *calc.Equip) { e.Refine, e.Grade = refine, grade })
		with := editSlots(at, slots, change)
		base := s.optScore(at, goal)
		if base <= 0 {
			return 0
		}
		return (s.optScore(with, goal)/base - 1) * 100
	}
	txt := func(g float64) string { return pctOf(1+g/100, l) }
	now := gain(e.Refine, e.Grade)
	var out []OptStep
	last, top := now, e.Refine
	if host.Refineable {
		for _, r := range stepRefines {
			if r <= e.Refine || r > calc.MaxRefine(slots[0]) || ctx.Err() != nil {
				continue
			}
			if g := gain(r, e.Grade); g-last >= optStepMin {
				out = append(out, OptStep{Refine: r, Grade: e.Grade, Gain: txt(g)})
				last, top = g, r
			}
		}
	}
	if host.Gradable {
		byRefine, last := last, now
		for gr := e.Grade + 1; gr <= 4 && ctx.Err() == nil; gr++ {
			if g := gain(e.Refine, gr); g-last >= optStepMin {
				out = append(out, OptStep{Refine: e.Refine, Grade: gr, Gain: txt(g)})
				last = g
			}
		}
		// Both together: some grade bonuses also ask for a refine.
		r := max(top, min(11, calc.MaxRefine(slots[0])))
		if host.Refineable && e.Grade < 4 && r > e.Refine && ctx.Err() == nil {
			if g := gain(r, 4); g-max(last, byRefine) >= optStepMin {
				out = append(out, OptStep{Refine: r, Grade: 4, Gain: txt(g)})
			}
		}
	}
	return out
}

// optTiers are the levels of the family in the position, with what each
// gives against the build as it is.
func (s *Server) optTiers(p optPosition, vals []float64, fam string, pct func(float64) string, kind, l string) []optOption {
	if p.item || p.opts != nil {
		return nil
	}
	var out []optOption
	for k, it := range p.pool {
		if vals[k] < 0 || optFamily(it.Name.Get(l)) != fam {
			continue
		}
		o := optOption{Item: viewItem(it, l), Kind: kind, Value: fmtInt(int64(vals[k] + 0.5)), Gain: pct(vals[k]), Slot: p.slots[0], Index: p.index}
		if it.ID == p.current {
			o.Gain = "="
		}
		o.Positive = strings.HasPrefix(o.Gain, "+")
		out = append(out, o)
	}
	// Two cards of one name are not levels.
	names := map[string]bool{}
	for _, o := range out {
		names[o.Item.Name] = true
	}
	if len(names) < 2 {
		return nil
	}
	sort.SliceStable(out, func(i, j int) bool { return tierNum(out[i].Item.Name) < tierNum(out[j].Item.Name) })
	return out
}

var reTierNum = regexp.MustCompile(`(\d+)\)?$`)

// tierNum is the number a name ends in ("(Grau Físico 5)" -> 5).
func tierNum(name string) int {
	m := reTierNum.FindStringSubmatch(strings.TrimSpace(name))
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// optDetails fills what only the options shown need: the refine and grade
// steps, and the best random options of the items suggested.
func (s *Server) optDetails(ctx context.Context, b calc.Build, res *optResult, o OptOptions, pct func(float64) string, l string) {
	type at struct{ g, k int }
	var list []at
	for gi := range res.Groups {
		for k := range res.Groups[gi].Options {
			list = append(list, at{gi, k})
		}
	}
	done := runPar(ctx, len(list), func(i int) {
		op := &res.Groups[list[i].g].Options[list[i].k]
		switch op.Kind {
		case "item":
			if !o.Opts {
				return
			}
			nb := s.swapAt(b, op.Slot, op.Item.ID, op.Refine, op.Grade)
			if rolls := s.bestOpts(nb, op.Slot, o.Goal); len(rolls) > 0 {
				v := s.optScore(withOpts(nb, op.Slot, rolls), o.Goal)
				op.Opts, op.OptsText = rolls, s.optsText(rolls, l)
				op.Value, op.Gain = fmtInt(int64(v+0.5)), pct(v)
			}
		case "card", "enchant":
			sb := b
			if op.Skill != "" { // measured on that skill, not on the attack
				id, _ := s.optHeldSkill(b, op.Item.ID, l)
				sb.Atk = calc.AttackChoice{Skill: id, Level: b.Skills[id]}
			}
			op.Steps = s.optSteps(ctx, sb, linkedSlots(b, op.Slot), op.Index, op.Item.ID, o.Goal, l)
		}
	})
	if !done {
		res.Partial = true
	}
	for gi := range res.Groups {
		g := &res.Groups[gi]
		if g.Kind == "item" { // the random options may change the order
			sort.SliceStable(g.Options, func(i, j int) bool { return gainPct(g.Options[i].Gain) > gainPct(g.Options[j].Gain) })
		}
		// An option that only pays off later must say when.
		g.Options = slices.DeleteFunc(g.Options, func(o optOption) bool { return o.Later && len(o.Steps) == 0 })
	}
}

func gainPct(t string) float64 {
	f, _ := strconv.ParseFloat(strings.NewReplacer("+", "", "%", "", ",", ".").Replace(t), 64)
	return f
}

// ---- Random options ----

// bestOpts fills the item's random options with the rolls (at their highest
// values) that score best, one position after the other. Options whose
// range is not known are not tried. nil when none helps.
func (s *Server) bestOpts(b calc.Build, slot, goal string) [][2]int {
	c := s.opt.Catalog
	spec := c.ItemOpts[b.Equip[slot].Item]
	if spec == nil || len(spec.Positions) == 0 {
		return nil
	}
	slots := linkedSlots(b, slot)
	b = editSlots(b, slots, func(e *calc.Equip) { e.Opts = nil })
	best := s.optScore(b, goal)
	var out [][2]int
	useful := 0
	for _, rolls := range spec.Positions {
		pick, score := [2]int{}, -1.0
		for _, r := range rolls {
			if r.Max == 0 || c.RandomOpts[r.ID] == nil {
				continue
			}
			v := [2]int{r.ID, r.Max}
			if sc := s.optScore(editSlots(b, slots, func(e *calc.Equip) { e.Opts = append(e.Opts, v) }), goal); sc > score {
				pick, score = v, sc
			}
		}
		if score < 0 {
			break // a position cannot be skipped
		}
		b = editSlots(b, slots, func(e *calc.Equip) { e.Opts = append(e.Opts, pick) })
		out = append(out, pick)
		if score > best*1.0005 {
			best, useful = score, len(out)
		}
	}
	return out[:useful]
}

// optsText lists random options ("ATQ +20 · CRIT +5").
func (s *Server) optsText(opts [][2]int, l string) string {
	var t []string
	for _, o := range opts {
		if ro := s.opt.Catalog.RandomOpts[o[0]]; ro != nil {
			t = append(t, ro.Label(l, o[1]))
		}
	}
	return strings.Join(t, " · ")
}

// withOpts is the build with the random options on the slot's item.
func withOpts(b calc.Build, slot string, opts [][2]int) calc.Build {
	if len(opts) == 0 || b.Equip[slot].Item == 0 {
		return b
	}
	return editSlots(b, linkedSlots(b, slot), func(e *calc.Equip) { e.Opts = slices.Clone(opts) })
}

// optRolls is a random option position's group.
func (s *Server) optRolls(res *optResult, g optGroup, p optPosition, vals []float64, base float64, b calc.Build, pct func(float64) string, l string) {
	c := s.opt.Catalog
	e := b.Equip[p.slots[0]]
	host := viewItem(c.Items[e.Item], l)
	if p.index < len(e.Opts) {
		cur := host
		cur.Name = s.optsText(e.Opts[p.index:p.index+1], l)
		g.Current = &cur
	}
	var idx []int
	for k, v := range vals {
		if v >= 0 {
			res.Tested++
			if v > base*1.0005 {
				idx = append(idx, k)
			}
		}
	}
	sort.SliceStable(idx, func(a, z int) bool { return vals[idx[a]] > vals[idx[z]] })
	for _, k := range idx[:min(len(idx), optTop)] {
		g.Options = append(g.Options, optOption{Item: host, Kind: "opt", Name: s.optsText(p.opts[k:k+1], l), Opt: p.opts[k],
			Value: fmtInt(int64(vals[k] + 0.5)), Gain: pct(vals[k]), Slot: g.Slot, Index: p.index, Positive: true})
	}
	g.Best = len(g.Options) == 0 && g.Current != nil
	res.Groups = append(res.Groups, g)
}

// SetOpt puts a random option at position i of the slot's item.
func (v *Views) SetOpt(b calc.Build, slot string, i int, opt [2]int) calc.Build {
	nb := editSlots(cloneBuild(b), linkedSlots(b, slot), func(e *calc.Equip) {
		if i < len(e.Opts) {
			e.Opts[i] = opt
		} else {
			e.Opts = append(e.Opts, opt)
		}
	})
	calc.Normalize(&nb, v.s.opt.Catalog)
	return nb
}

// SwapAtOpts is SwapAt with random options on the new item.
func (v *Views) SwapAtOpts(b calc.Build, slot string, id, refine, grade int, opts [][2]int) calc.Build {
	nb := withOpts(v.s.swapAt(b, slot, id, refine, grade), slot, opts)
	calc.Normalize(&nb, v.s.opt.Catalog)
	return nb
}

// ---- Costumes ----

// isStone reports a costume's enchant stone.
func isStone(it *gamedata.Item) bool {
	return it != nil && it.Type == "Card" && slices.ContainsFunc(it.Locations, calc.IsCostume)
}

// stoneIndex is the position a costume's stone sits in (the first one when
// it has none: any counts).
func stoneIndex(e calc.Equip) int {
	for i, cd := range e.Cards {
		if cd != 0 {
			return i
		}
	}
	return 0
}

// stoneSlots are the costume slots worn, and the labels of the empty ones.
func (s *Server) stoneSlots(b calc.Build, l string) (worn, empty []string) {
	for _, info := range costumeSlots {
		if s.opt.Catalog.Items[b.Equip[info.Key].Item] != nil {
			worn = append(worn, info.Key)
		} else {
			empty = append(empty, tr(l, info.Label))
		}
	}
	return worn, empty
}

// stones are the stones a costume slot takes.
func (s *Server) stones(slot string) []*gamedata.Item {
	var out []*gamedata.Item
	for _, it := range s.opt.Catalog.Items {
		if isStone(it) && calc.CardFits(it, slot) && it.Name.Get("pt") != "" {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// stoneSets finds the sets of costume stones that score best: every set
// whose stones fit the costumes worn, the best of them with the other
// costumes' stones chosen again.
func (s *Server) stoneSets(ctx context.Context, b calc.Build, goal, l string) (opts []optOption, tested int) {
	c := s.opt.Catalog
	worn, _ := s.stoneSlots(b, l)
	base := s.optScore(b, goal)
	if len(worn) < 2 || base <= 0 {
		return nil, 0
	}
	pools := map[string][]*gamedata.Item{}
	slotOf := map[int]string{}
	for _, sl := range worn {
		pools[sl] = s.stones(sl)
		for _, it := range pools[sl] {
			slotOf[it.ID] = sl
		}
	}
	type set struct {
		at    map[string]int // slot -> stone
		score float64
	}
	var sets []set
	for _, cb := range c.Combos {
		at := map[string]int{}
		ok := len(cb.Items) >= 2
		for _, id := range cb.Items {
			sl, fits := slotOf[id]
			if _, dup := at[sl]; !fits || dup {
				ok = false
				break
			}
			at[sl] = id
		}
		if ok {
			sets = append(sets, set{at: at})
		}
	}
	with := func(at map[string]int) calc.Build {
		nb := b
		nb.Equip = maps.Clone(b.Equip)
		for sl, id := range at {
			e := nb.Equip[sl]
			e.Cards[stoneIndex(b.Equip[sl])] = id
			nb.Equip[sl] = e
		}
		return nb
	}
	runPar(ctx, len(sets), func(i int) { sets[i].score = s.optScore(with(sets[i].at), goal) })
	tested = len(sets)
	sort.SliceStable(sets, func(i, j int) bool { return sets[i].score > sets[j].score })
	sets = sets[:min(len(sets), 12)]
	runPar(ctx, len(sets), func(i int) {
		st := &sets[i]
		seed := maps.Clone(st.at)
		for range 2 { // twice: what they complete together adds up
			for _, sl := range worn {
				if seed[sl] != 0 {
					continue
				}
				for _, it := range pools[sl] {
					if ctx.Err() != nil {
						return
					}
					at := maps.Clone(st.at)
					at[sl] = it.ID
					if sc := s.optScore(with(at), goal); sc > st.score*1.00001 {
						st.at, st.score = at, sc
					}
				}
			}
		}
	})
	sort.SliceStable(sets, func(i, j int) bool { return sets[i].score > sets[j].score })
	seen := map[string]bool{}
	for _, st := range sets {
		if st.score <= base*1.0005 || len(opts) >= optTop {
			break
		}
		var parts []ComboPart
		var names []string
		for _, sl := range worn {
			id, idx := st.at[sl], stoneIndex(b.Equip[sl])
			if id == 0 || b.Equip[sl].Cards[idx] == id {
				continue
			}
			n := c.Items[id].Name.Get(l)
			parts = append(parts, ComboPart{Slot: sl, Idx: idx, Card: id, CardName: n, SlotLabel: tr(l, slotLabel(sl))})
			names = append(names, n)
		}
		key := strings.Join(names, " + ")
		if len(parts) < 2 || seen[key] {
			continue // a single stone is in its own group
		}
		seen[key] = true
		opts = append(opts, optOption{Item: viewItem(c.Items[parts[0].Card], l), Kind: "set", Name: key,
			Value: fmtInt(int64(st.score + 0.5)), Gain: pctOf(st.score/base, l), Slot: "set", Index: len(opts), Parts: parts, Positive: true})
	}
	return opts, tested
}

// ApplyParts puts each card where the part says.
func (v *Views) ApplyParts(b calc.Build, parts []ComboPart) calc.Build {
	nb := cloneBuild(b)
	for _, p := range parts {
		nb = v.s.swapItem(nb, p.Slot, p.Card, p.Idx)
	}
	return nb
}
