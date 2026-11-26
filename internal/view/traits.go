package view

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"ragnarok-of-building/internal/calc"
	"ragnarok-of-building/internal/gamedata"
)

// Comparing trait distributions (Comparar talentos). Some enchants pay per
// N points of a base trait (Constelação, Poder de Varmunt...), so a
// distribution is only worth what it is with its own best enchants: every
// column picks again the trait enchants of the gear worn.

const (
	traitTimeout = 20 * time.Second
	traitColumns = 3 // pairs shown beside the current build and the best found
)

// TraitOptions are the comparison's choices.
type TraitOptions struct {
	Goal     string // GoalDamage, GoalPhysDef, GoalMagDef
	AnyLevel bool   // also other levels of the enchants (and positions without one)
}

// TraitEnchant is a trait enchant in a column.
type TraitEnchant struct {
	Slot      string
	Idx       int
	Card      int
	Name      string
	SlotLabel string
	Changed   bool // differs from the build's
}

// TraitCol is one distribution with its enchants.
type TraitCol struct {
	Title    string
	Traits   [6]int
	Enchants []TraitEnchant
	Ratio    float64 // score against the current build
	Gain     StatLine
	Build    calc.Build // to apply
}

// TraitView is the comparison.
type TraitView struct {
	NoTarget  bool
	NotFourth bool
	NoDamage  bool // the attack does nothing to the target (wrong weapon, immune element)
	Target    string
	Budget    int // points the level gives, which the columns spend
	Used      int // points the build spends
	Partial   bool
	Tested    int
	Cols      []TraitCol // [0] = the build as it is; the last = the best found
	Rows      []CompareRow
}

var (
	reTraitParam = regexp.MustCompile(`readparam\(b(?:Pow|Sta|Wis|Spl|Con|Crt)\)`)
	reEnchLevel  = regexp.MustCompile(`(\d+)\s*$`)
)

// traitEnchant reports an enchant whose bonus depends on a base trait.
func traitEnchant(it *gamedata.Item) bool {
	return it != nil && it.Type == "Card" && it.SubType == "Enchant" && reTraitParam.MatchString(it.Script+it.DescScript)
}

// enchLevel is the level in an enchant's name ("Constelação do Poder 2"); 0
// without one.
func enchLevel(it *gamedata.Item) int {
	n := 0
	if m := reEnchLevel.FindStringSubmatch(it.Name.Get("pt")); m != nil {
		fmt.Sscan(m[1], &n)
	}
	return n
}

// traitPos is an enchant position and the trait enchants it may take.
type traitPos struct {
	slots []string // linked headgear slots hold the same item
	idx   int
	cur   int
	opts  []int
}

func (s *Server) traitPositions(b calc.Build, anyLevel bool) []traitPos {
	c := s.opt.Catalog
	var out []traitPos
	done := map[string]bool{}
	for _, slot := range calc.Slots {
		e, ok := b.Equip[slot]
		it := c.Items[e.Item]
		if !ok || it == nil || done[slot] {
			continue
		}
		linked := []string{slot}
		if strings.HasPrefix(slot, "Head_") {
			for _, h := range []string{calc.HeadTop, calc.HeadMid, calc.HeadLow} {
				if h != slot && b.Equip[h].Item == e.Item {
					linked = append(linked, h)
				}
			}
		}
		for _, k := range linked {
			done[k] = true
		}
		for idx := it.Slots; idx < 4; idx++ {
			cur := c.Items[e.Cards[idx]]
			isTrait := traitEnchant(cur)
			if !isTrait && !anyLevel {
				continue
			}
			p := traitPos{slots: linked, idx: idx, cur: e.Cards[idx]}
			for _, id := range c.Enchants[it.ID][idx] {
				en := c.Items[id]
				if !traitEnchant(en) || (!anyLevel && enchLevel(en) != enchLevel(cur)) {
					continue
				}
				p.opts = append(p.opts, id)
			}
			if len(p.opts) > 0 {
				if !slices.Contains(p.opts, p.cur) {
					p.opts = append([]int{p.cur}, p.opts...)
				}
				out = append(out, p)
			}
		}
	}
	return out
}

// traitSearch scores distributions, each with its own best enchants.
type traitSearch struct {
	s    *Server
	ctx  context.Context
	b    calc.Build
	goal string
	pos  []traitPos
	max  int

	mu   sync.Mutex
	seen map[[6]int]traitScore
}

type traitScore struct {
	score float64
	cards []int // per pos
}

// with is the build with the traits and enchants.
func (t *traitSearch) with(tr [6]int, cards []int) calc.Build {
	nb := cloneBuild(t.b)
	nb.Traits = tr
	for i, p := range t.pos {
		for _, k := range p.slots {
			e := nb.Equip[k]
			e.Cards[p.idx] = cards[i]
			nb.Equip[k] = e
		}
	}
	return nb
}

// eval picks the enchants position by position, twice (they add to the same
// percentages, so one pass may be off).
func (t *traitSearch) eval(tr [6]int) traitScore {
	t.mu.Lock()
	sc, ok := t.seen[tr]
	t.mu.Unlock()
	if ok {
		return sc
	}
	cards := make([]int, len(t.pos))
	for i, p := range t.pos {
		cards[i] = p.cur
	}
	best := t.s.optScore(t.with(tr, cards), t.goal)
	for pass := 0; pass < 2; pass++ {
		changed := false
		for i, p := range t.pos {
			for _, id := range p.opts {
				if id == cards[i] || t.ctx.Err() != nil {
					continue
				}
				old := cards[i]
				cards[i] = id
				if x := t.s.optScore(t.with(tr, cards), t.goal); x > best*1.000001 {
					best, changed = x, true
				} else {
					cards[i] = old
				}
			}
		}
		if !changed {
			break
		}
	}
	sc = traitScore{best, cards}
	if t.ctx.Err() == nil {
		t.mu.Lock()
		t.seen[tr] = sc
		t.mu.Unlock()
	}
	return sc
}

// evalAll scores the distributions in parallel, best first.
func (t *traitSearch) evalAll(list [][6]int) [][6]int {
	ch := make(chan [6]int)
	var wg sync.WaitGroup
	for range max(1, runtime.NumCPU()-1) {
		wg.Go(func() {
			for tr := range ch {
				yield()
				t.eval(tr)
			}
		})
	}
	for _, tr := range list {
		if t.ctx.Err() != nil {
			break
		}
		ch <- tr
	}
	close(ch)
	wg.Wait()
	var out [][6]int
	for _, tr := range list {
		if _, ok := t.seen[tr]; ok && !slices.Contains(out, tr) {
			out = append(out, tr)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return t.seen[out[i]].score > t.seen[out[j]].score })
	return out
}

// pairs are the distributions that fill two traits, then a third with what
// is left.
func (t *traitSearch) pairs(budget int) [][6]int {
	var out [][6]int
	for i := range 6 {
		for j := range 6 {
			if i == j {
				continue
			}
			var tr [6]int
			tr[i] = min(t.max, budget)
			tr[j] = min(t.max, budget-tr[i])
			left := budget - tr[i] - tr[j]
			if left <= 0 {
				out = append(out, tr)
				continue
			}
			for k := range 6 {
				if k != i && k != j {
					x := tr
					x[k] = min(t.max, left)
					out = append(out, x)
				}
			}
		}
	}
	return out
}

// filled are the distributions that put the points left on a build's
// traits: each trait first, then the others in order.
func (t *traitSearch) filled(tr [6]int, left int) [][6]int {
	var out [][6]int
	for i := range 6 {
		x, l := tr, left
		for k := range 6 {
			j := (i + k) % 6
			n := min(l, t.max-x[j])
			x[j] += n
			l -= n
		}
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// climb moves points between traits while it pays: by the enchants' steps
// (15, 20), and finer.
func (t *traitSearch) climb(tr [6]int) [6]int {
	for t.ctx.Err() == nil {
		var next [][6]int
		for _, step := range []int{40, 20, 15, 10, 7, 5, 3, 1} {
			for i := range 6 {
				for j := range 6 {
					if i == j || tr[i] < step || tr[j]+step > t.max {
						continue
					}
					x := tr
					x[i], x[j] = x[i]-step, x[j]+step
					next = append(next, x)
				}
			}
		}
		ranked := t.evalAll(next)
		if len(ranked) == 0 || t.seen[ranked[0]].score <= t.eval(tr).score*1.00001 {
			return tr
		}
		tr = ranked[0]
	}
	return tr
}

// mainTraits are the two biggest traits (the pair a column is named after).
func mainTraits(tr [6]int) [2]int {
	idx := []int{0, 1, 2, 3, 4, 5}
	sort.SliceStable(idx, func(i, j int) bool { return tr[idx[i]] > tr[idx[j]] })
	p := [2]int{idx[0], idx[1]}
	if p[0] > p[1] {
		p[0], p[1] = p[1], p[0]
	}
	return p
}

// traitBudget is what the level gives, even when the build spends more.
func traitBudget(b calc.Build, r calc.Result) int {
	return max(0, traitsUsed(b)+r.TraitPoints)
}

func traitsUsed(b calc.Build) int {
	used := 0
	for _, v := range b.Traits {
		used += v
	}
	return used
}

// CompareTraits compares the build's traits with the best pairs of traits
// and the best distribution found, each with its own trait enchants.
func (v *Views) CompareTraits(ctx context.Context, b calc.Build, o TraitOptions, l string) TraitView {
	s := v.s
	c := s.opt.Catalog
	b = cloneBuild(b)
	calc.Normalize(&b, c)
	var out TraitView
	if j := c.Jobs[b.Job]; j == nil || !j.Fourth {
		out.NotFourth = true
		return out
	}
	m := c.Monster(b.Target)
	if m == nil {
		out.NoTarget = true
		return out
	}
	out.Target = m.NameIn(l)
	if o.Goal == "" {
		o.Goal = GoalDamage
	}
	r := calc.Compute(b, c)
	out.Budget, out.Used = traitBudget(b, r), traitsUsed(b)
	ctx, cancel := context.WithTimeout(ctx, traitTimeout)
	defer cancel()
	t := &traitSearch{s: s, ctx: ctx, b: b, goal: o.Goal, pos: s.traitPositions(b, o.AnyLevel), max: r.MaxTrait, seen: map[[6]int]traitScore{}}
	base := s.optScore(b, o.Goal)
	if base <= 0 {
		out.NoDamage = true
		return out
	}
	list := t.pairs(out.Budget)
	if left := out.Budget - out.Used; left == 0 {
		list = append(list, b.Traits)
	} else if left > 0 {
		list = append(list, t.filled(b.Traits, left)...)
	}
	ranked := t.evalAll(list)

	cur := make([]int, len(t.pos))
	for i, p := range t.pos {
		cur[i] = p.cur
	}
	col := func(title string, tr [6]int, sc traitScore) TraitCol {
		cl := TraitCol{Title: title, Traits: tr, Ratio: sc.score / base, Build: t.with(tr, sc.cards)}
		cl.Gain = StatLine{Diff: pctChange(cl.Ratio, l), Better: cl.Ratio > 1.0005, Same: cl.Ratio > 0.9995 && cl.Ratio < 1.0005}
		if cl.Gain.Same {
			cl.Gain.Diff = "="
		}
		for i, p := range t.pos {
			if it := c.Items[sc.cards[i]]; it != nil {
				cl.Enchants = append(cl.Enchants, TraitEnchant{Slot: p.slots[0], Idx: p.idx, Card: it.ID, Name: it.Name.Get(l),
					SlotLabel: tr2(l, p.slots[0]), Changed: sc.cards[i] != p.cur})
			}
		}
		return cl
	}
	out.Cols = append(out.Cols, col(tr(l, "Atual"), b.Traits, traitScore{base, cur}))
	seen := map[[2]int]bool{}
	for _, x := range ranked {
		p := mainTraits(x)
		// The build's own traits are a column too when other enchants serve
		// them better: without it, a gain that comes from the enchants would
		// read as coming from the other columns' traits.
		if seen[p] || x == b.Traits && slices.Equal(t.eval(x).cards, cur) {
			continue
		}
		seen[p] = true
		out.Cols = append(out.Cols, col(tr(l, traitNames[p[0]])+" + "+tr(l, traitNames[p[1]]), x, t.eval(x)))
		if len(out.Cols) > traitColumns {
			break
		}
	}
	if len(ranked) > 0 {
		best := ranked[0]
		for _, start := range ranked[:min(3, len(ranked))] {
			if x := t.climb(start); t.eval(x).score > t.eval(best).score {
				best = x
			}
		}
		out.Cols = append(out.Cols, col(tr(l, "Melhor achado"), best, t.eval(best)))
	}
	out.Partial, out.Tested = ctx.Err() != nil, len(t.seen)
	out.Rows = s.traitRows(out.Cols, o.Goal, l)
	return out
}

// tr2 is a slot's label in the language.
func tr2(l, slot string) string { return tr(l, slotLabel(slot)) }

// traitRows are the numbers that differ between the columns.
func (s *Server) traitRows(cols []TraitCol, goal, l string) []CompareRow {
	c := s.opt.Catalog
	type stat struct {
		label string
		get   func(calc.Result) float64
	}
	stats := []stat{
		{"ATQ", func(r calc.Result) float64 { return float64(r.StatusATK + r.EquipATK) }},
		{"ATQM", func(r calc.Result) float64 { return float64(r.MATKMax + r.EquipMATK) }},
		{"P.ATQ", func(r calc.Result) float64 { return float64(r.PATK) }},
		{"S.ATQM", func(r calc.Result) float64 { return float64(r.SMATK) }},
		{"C.RATE", func(r calc.Result) float64 { return float64(r.CRATE) }},
		{"Dano crítico %", func(r calc.Result) float64 { return float64(r.Bonuses["bCritAtkRate"]) }},
		{"À distância %", func(r calc.Result) float64 { return float64(r.Bonuses["bLongAtkRate"]) }},
		{"Corpo a corpo %", func(r calc.Result) float64 { return float64(r.Bonuses["bShortAtkRate"]) }},
		{"Precisão", func(r calc.Result) float64 { return float64(r.HIT) }},
		{"RES", func(r calc.Result) float64 { return float64(r.RES) }},
		{"MRES", func(r calc.Result) float64 { return float64(r.MRES) }},
		{"H.PLUS", func(r calc.Result) float64 { return float64(r.HPLUS) }},
		{"HP", func(r calc.Result) float64 { return float64(r.HP) }},
		{"SP", func(r calc.Result) float64 { return float64(r.SP) }},
	}
	var rs []calc.Result
	for _, cl := range cols {
		rs = append(rs, calc.Compute(cl.Build, c))
	}
	if len(rs) == 0 {
		return nil
	}
	label := "Dano esperado"
	switch goal {
	case GoalPhysDef:
		label = "HP efetivo (físico)"
	case GoalMagDef:
		label = "HP efetivo (mágico)"
	}
	row := func(lb string, main bool, get func(i int) float64) CompareRow {
		out := CompareRow{Label: tr(l, lb), Main: main}
		x0 := get(0)
		for i := range cols {
			x := get(i)
			out.Values = append(out.Values, fmtInt(int64(math.Round(x))))
			d, better := "", 0
			if i > 0 && x != x0 {
				better = -1
				if x > x0 {
					better = 1
				}
				if main && x0 > 0 {
					if d = pctChange(x/x0, l); x/x0 > 0.9995 && x/x0 < 1.0005 {
						d, better = "", 0
					}
				} else if d = fmtInt(int64(math.Round(x - x0))); x > x0 {
					d = "+" + d
				}
			}
			out.Diffs, out.Better = append(out.Diffs, d), append(out.Better, better)
		}
		return out
	}
	out := []CompareRow{row(label, true, func(i int) float64 { return s.optScore(cols[i].Build, goal) })}
	for _, st := range stats {
		differs := false
		for _, r := range rs[1:] {
			differs = differs || st.get(r) != st.get(rs[0])
		}
		if differs {
			out = append(out, row(st.label, false, func(i int) float64 { return st.get(rs[i]) }))
		}
	}
	return out
}

// ApplyTraits puts a column's traits and enchants on the build.
func (v *Views) ApplyTraits(b calc.Build, tr [6]int, enc []TraitEnchant) calc.Build {
	nb := cloneBuild(b)
	for _, e := range enc {
		if e.Changed {
			nb = v.s.swapItem(nb, e.Slot, e.Card, e.Idx)
		}
	}
	nb.Traits = tr
	calc.Normalize(&nb, v.s.opt.Catalog)
	return nb
}

// traitAlternatives ranks the position's trait enchants with the traits
// changed for them (the rest of the build as it is): rows of Melhor carta.
func (s *Server) traitAlternatives(ctx context.Context, b calc.Build, slot string, idx int, ids []int, goal string, base float64, l string) []BestItem {
	c := s.opt.Catalog
	if j := c.Jobs[b.Job]; j == nil || !j.Fourth {
		return nil
	}
	r := calc.Compute(b, c)
	budget := traitBudget(b, r)
	var out []BestItem
	for _, id := range ids {
		if !traitEnchant(c.Items[id]) || ctx.Err() != nil {
			continue
		}
		nb := s.swapItem(b, slot, id, idx)
		if nb.Equip[slot].Cards[idx] != id {
			continue
		}
		t := &traitSearch{s: s, ctx: ctx, b: nb, goal: goal, max: r.MaxTrait, seen: map[[6]int]traitScore{}}
		ranked := t.evalAll(t.pairs(budget))
		if len(ranked) == 0 || ranked[0] == b.Traits {
			continue
		}
		ratio := t.seen[ranked[0]].score / base
		if ratio <= 1.0005 || ratio <= s.optScore(nb, goal)/base*1.0005 {
			continue // the traits as they are do as well
		}
		best := ranked[0]
		out = append(out, BestItem{Item: viewItem(c.Items[id], l), Ratio: ratio, Traits: &best,
			Gain: StatLine{Diff: pctChange(ratio, l), Better: true}})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ratio > out[j].Ratio })
	return out[:min(len(out), 5)]
}
