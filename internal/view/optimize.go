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

// Optimizer, part 1: for every card slot (and every enchant position in use)
// of the equipped items, try each card that fits, one at a time, and rank them
// by the expected damage against the damage window's target with its attack.

const (
	optTop     = 5               // suggestions per position
	optTimeout = 8 * time.Second // the rest of the positions are skipped after this
)

// OptOptions say what the optimizer tries and what it raises.
type OptOptions struct {
	Goal                   string // GoalDamage (default), GoalPhysDef, GoalMagDef
	Items, Cards, Enchants bool
	NoMVP                  bool // leave MVP cards out
	Refine, Grade          int // of the items it suggests (those that take them)
	Opts                   bool // random options of the worn items; best rolls on the items it suggests
	Costumes               bool // enchant stones of the worn costumes, and their sets
	Traits                 bool // also search trait distributions (4th classes)
}

type optOption struct {
	Item     itemView
	Kind     string // "item", "card" or "enchant"
	Refine   int    // items: the refine and grade it was measured at
	Grade    int
	Value    string // expected damage
	Gain     string // "+18,4%"
	Skill    string // skill enchants: the skill the gain is measured on
	Slot     string
	Index    int
	Positive bool
	Name     string      // what it is, when it is not just the item (random options, sets)
	Tiers    []optOption // the family's levels ("Grau Físico" 1 to 5), lowest first
	Steps    []OptStep   // what it gives at higher refines and grades of its item
	Tag      string      // "carta de <skill>": listed for the attack chosen
	Later    bool        // it only pays off at one of the Steps
	Opt      [2]int      // Kind "opt": the random option and value
	Opts     [][2]int    // items: the random options it was measured with
	OptsText string
	Parts    []ComboPart // Kind "set": the stones and where they go
}

type optGroup struct {
	Kind        string // "item", "card" or "enchant"
	Slot, Label string
	Index       int
	Enchant     bool
	Current     *itemView
	Options     []optOption
	// With no options: Best = the current card/enchant already gives the
	// most; otherwise nothing in the pool adds damage to this attack, and
	// SkillOnly says the pool is skill enchants (pick that skill to see them).
	Best      bool
	SkillOnly bool
	// SkillHeld: the position holds an enchant for this learned skill; a
	// swap to another skill's enchant can't be judged without knowing the
	// main skill, so none is suggested.
	SkillHeld string
}

type optResult struct {
	Goal     string
	NoTarget bool
	NoItems  bool
	Base     string
	Target   string
	Attack   string
	Groups   []optGroup
	NoEnch   []string // enchanted items the client's enchant list doesn't cover
	Tested   int
	Partial  bool // stopped by the time limit
	// Traits: the best trait distribution found, with its enchants; nil when
	// none beats the build's.
	Traits *TraitCol
	// NoSlot: card sets for the attack's skill that the items worn have no
	// slot for.
	NoSlot []string
	// NoCostume: the costume slots with nothing worn (no stones to test).
	NoCostume []string
}

// optPosition is one card position to optimize.
type optPosition struct {
	item    bool     // the slot's item itself (index -1)
	slots   []string // linked slots (one headgear covering several)
	label   string
	index   int
	enchant bool
	current int
	pool    []*gamedata.Item
	// Random options: the rolls tried at position index of the item.
	opts [][2]int
}

func (p *optPosition) size() int {
	if p.opts != nil {
		return len(p.opts)
	}
	return len(p.pool)
}

// build is the build with candidate k in the position (cards, enchants and
// random options).
func (p *optPosition) build(b calc.Build, k int) calc.Build {
	return editSlots(b, p.slots, func(e *calc.Equip) {
		switch {
		case p.opts == nil:
			e.Cards[p.index] = p.pool[k].ID
		case p.index < len(e.Opts):
			e.Opts[p.index] = p.opts[k]
		default:
			e.Opts = append(e.Opts, p.opts[k])
		}
	})
}

// optCards lists the cards once (the catalog never changes).
func (s *Server) optCards() []*gamedata.Item {
	s.optOnce.Do(func() {
		for _, it := range s.opt.Catalog.Items {
			if it.Type == "Card" && it.SubType != "Enchant" && it.Name.Get("pt") != "" {
				s.optCardList = append(s.optCardList, it)
			}
		}
		sort.Slice(s.optCardList, func(i, j int) bool { return s.optCardList[i].ID < s.optCardList[j].ID })
	})
	return s.optCardList
}

// optExpected is the objective: expected damage per use of the attack. The
// build must already be normalized (Normalize is not safe to run in parallel
// on builds sharing maps).
func (s *Server) optExpected(b calc.Build) float64 {
	r := calc.Compute(b, s.opt.Catalog)
	return s.damageView(b, r, "pt").Dmg.Expected
}

func (s *Server) optimize(ctx context.Context, b calc.Build, enchants bool, l string) optResult {
	return s.optimizeWith(ctx, b, OptOptions{Goal: GoalDamage, Cards: true, Enchants: enchants}, l)
}

func (s *Server) optimizeWith(ctx context.Context, b calc.Build, o OptOptions, l string) optResult {
	c := s.opt.Catalog
	if o.Goal == "" {
		o.Goal = GoalDamage
	}
	enchants := o.Enchants
	// A copy: Normalize and the swaps write to the maps, the caller's.
	b = cloneBuild(b)
	calc.Normalize(&b, c)
	res := optResult{Goal: o.Goal}
	m := c.Monster(b.Target)
	if m == nil {
		res.NoTarget = true
		return res
	}
	res.Target = m.NameIn(l)
	res.Attack = tr(l, "Ataque básico")
	if sk := c.Skills[b.Atk.Skill]; sk != nil {
		res.Attack = sk.Name.Get(l) + " " + tr(l, "Nv.") + " " + strconv.Itoa(b.Atk.Level)
	}
	base := s.optScore(b, o.Goal)
	res.Base = fmtInt(int64(base + 0.5))

	code := ""
	if o.Goal == GoalDamage {
		code = s.attackCode(b)
	}
	skillTag := ""
	if sk := c.Skills[b.Atk.Skill]; sk != nil {
		skillTag = tr(l, "carta de") + " " + sk.Name.Get(l)
	}
	cards := s.optCards()
	var pos []optPosition
	if o.Items {
		job := c.Jobs[b.Job]
		for _, info := range append(append([]slotInfo{}, equipSlots...), shadowSlots...) {
			if info.Key == calc.Shield && calc.ShieldBlocked(b, c) {
				continue
			}
			var pool []*gamedata.Item
			for _, it := range c.Items {
				if it.Type != "" && it.Type != "Card" && calc.Fits(it, info.Key) && job.CanEquip(it) && it.Name.Get(l) != "" {
					pool = append(pool, it)
				}
			}
			sort.Slice(pool, func(i, j int) bool { return pool[i].ID < pool[j].ID })
			if len(pool) > 0 {
				pos = append(pos, optPosition{item: true, slots: []string{info.Key}, label: info.Label, index: -1,
					current: b.Equip[info.Key].Item, pool: pool})
			}
		}
	}
	done := map[string]bool{}
	for _, info := range append(append([]slotInfo{}, equipSlots...), shadowSlots...) {
		e, ok := b.Equip[info.Key]
		it := c.Items[e.Item]
		if !ok || it == nil || done[info.Key] {
			continue
		}
		linked := []string{info.Key}
		if strings.HasPrefix(info.Key, "Head_") {
			for _, h := range []string{calc.HeadTop, calc.HeadMid, calc.HeadLow} {
				if h != info.Key && b.Equip[h].Item == e.Item {
					linked = append(linked, h)
				}
			}
		}
		for _, k := range linked {
			done[k] = true
		}
		var pool []*gamedata.Item
		for _, cd := range cards {
			if calc.CardFits(cd, info.Key) && !(o.NoMVP && cd.MVPCard) {
				pool = append(pool, cd)
			}
		}
		// Enchants: only what the client's enchant list gives this item.
		ench := c.Enchants[it.ID]
		for i := 0; i < 4; i++ {
			p := optPosition{slots: linked, label: info.Label, index: i, current: e.Cards[i]}
			switch {
			case i < it.Slots:
				if !o.Cards {
					continue
				}
				p.pool = pool
			case enchants && len(ench[i]) > 0:
				p.enchant = true
				for _, id := range ench[i] {
					if ei := c.Items[id]; ei != nil && !calc.EnchantBlocked(it.ID, i, e.Cards, id) {
						p.pool = append(p.pool, ei)
					}
				}
			default:
				if enchants && ench == nil && e.Cards[i] != 0 {
					if name := it.Name.Get(l); !slices.Contains(res.NoEnch, name) {
						res.NoEnch = append(res.NoEnch, name)
					}
				}
				continue
			}
			pos = append(pos, p)
		}
		// Random options: the rolls in use and the next free one (a position
		// cannot be skipped). Only those whose range is known.
		if spec := c.ItemOpts[it.ID]; o.Opts && spec != nil {
			for i := 0; i < len(spec.Positions) && i <= len(e.Opts); i++ {
				p := optPosition{slots: linked, label: info.Label, index: i}
				cur := [2]int{}
				if i < len(e.Opts) {
					cur = e.Opts[i]
				}
				for _, r := range spec.Positions[i] {
					if r.Max != 0 && c.RandomOpts[r.ID] != nil && (cur[0] != r.ID || cur[1] < r.Max) {
						p.opts = append(p.opts, [2]int{r.ID, r.Max})
					}
				}
				if len(p.opts) > 0 {
					pos = append(pos, p)
				}
			}
		}
	}
	if o.Costumes {
		var worn []string
		worn, res.NoCostume = s.stoneSlots(b, l)
		for _, sl := range worn {
			e := b.Equip[sl]
			i := stoneIndex(e)
			pos = append(pos, optPosition{slots: []string{sl}, label: slotLabel(sl), index: i, enchant: true,
				current: e.Cards[i], pool: s.stones(sl)})
		}
	}
	if len(pos) == 0 {
		res.NoItems = true
		return res
	}

	// Evaluate every (position, card) in parallel.
	type job struct{ p, k int }
	vals := make([][]float64, len(pos))
	later := make([][]float64, len(pos)) // enchants: at a high refine and grade
	var jobs []job
	for pi, p := range pos {
		vals[pi] = make([]float64, p.size())
		later[pi] = make([]float64, p.size())
		for k := range p.size() {
			vals[pi][k] = -1
			jobs = append(jobs, job{pi, k})
		}
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, optTimeout)
	defer cancel()
	ch := make(chan job)
	var wg sync.WaitGroup
	for w := 0; w < max(1, runtime.NumCPU()-1); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				yield()
				p := pos[j.p]
				if p.item {
					if p.pool[j.k].ID == p.current {
						continue // the item itself (at another refine) is not a swap
					}
					vals[j.p][j.k] = s.optScore(s.swapAt(b, p.slots[0], p.pool[j.k].ID, o.Refine, o.Grade), o.Goal)
					continue
				}
				nb := p.build(b, j.k)
				vals[j.p][j.k] = s.optScore(nb, o.Goal)
				if p.opts == nil && (p.enchant || boostsSkill(p.pool[j.k], code)) && vals[j.p][j.k] <= base*1.0005 && scalesWithItem(p.pool[j.k]) {
					host := c.Items[b.Equip[p.slots[0]].Item]
					up := func(e *calc.Equip) {
						if host.Refineable {
							e.Refine = max(e.Refine, min(laterRefine, calc.MaxRefine(p.slots[0])))
						}
						if host.Gradable {
							e.Grade = 4
						}
					}
					if hb := s.optScore(editSlots(b, p.slots, up), o.Goal); hb > 0 {
						later[j.p][j.k] = s.optScore(editSlots(nb, p.slots, up), o.Goal) / hb
					}
				}
			}
		}()
	}
feed:
	for _, j := range jobs {
		select {
		case ch <- j:
		case <-ctx.Done():
			res.Partial = true
			break feed
		}
	}
	close(ch)
	wg.Wait()

	pct := func(v float64) string {
		if base <= 0 {
			return ""
		}
		g := (v/base - 1) * 100
		txt := strconv.FormatFloat(g, 'f', 1, 64)
		if l != "en" {
			txt = strings.Replace(txt, ".", ",", 1)
		}
		if g >= 0 {
			txt = "+" + txt
		}
		return txt + "%"
	}
	for pi, p := range pos {
		label := tr(l, p.label) + " · " + tr(l, "Carta") + " " + strconv.Itoa(p.index+1)
		kind := "card"
		switch {
		case p.item:
			label, kind = tr(l, p.label), "item"
		case p.enchant:
			label, kind = tr(l, p.label)+" · "+tr(l, "Encantamento")+" (slot "+strconv.Itoa(p.index+1)+")", "enchant"
			if calc.IsCostume(p.slots[0]) {
				label = tr(l, p.label) + " · " + tr(l, "Encantamento")
			}
		case p.opts != nil:
			label, kind = tr(l, p.label)+" · "+tr(l, "Bônus aleatório")+" "+strconv.Itoa(p.index+1), "opt"
		}
		g := optGroup{Kind: kind, Slot: p.slots[0], Label: label, Index: p.index, Enchant: p.enchant}
		if ci := c.Items[p.current]; p.current != 0 && ci != nil {
			v := viewItem(ci, l)
			g.Current = &v
		}
		if kind == "opt" {
			s.optRolls(&res, g, p, vals[pi], base, b, pct, l)
			continue
		}
		idx := make([]int, 0, len(p.pool))
		for k, v := range vals[pi] {
			if v >= 0 {
				res.Tested++
				if v > base*1.0005 && p.pool[k].ID != p.current {
					idx = append(idx, k)
				}
			}
		}
		sort.SliceStable(idx, func(a, z int) bool { return vals[pi][idx[a]] > vals[pi][idx[z]] })
		seen := map[string]bool{}
		for _, k := range idx {
			if len(g.Options) >= optTop {
				break
			}
			// Tiers of one card/enchant ("… 10", "… 9") show only the best.
			fam := optFamily(p.pool[k].Name.Get(l))
			if seen[fam] {
				continue
			}
			seen[fam] = true
			opt := optOption{Tiers: s.optTiers(p, vals[pi], fam, pct, kind, l), Item: viewItem(p.pool[k], l), Kind: kind, Value: fmtInt(int64(vals[pi][k] + 0.5)),
				Gain: pct(vals[pi][k]), Slot: g.Slot, Index: p.index, Positive: true}
			if p.item {
				// What it was measured at: Normalize drops what the item can't take.
				if p.pool[k].Refineable {
					opt.Refine = min(o.Refine, calc.MaxRefine(g.Slot))
				}
				if p.pool[k].Gradable {
					opt.Grade = o.Grade
				}
			}
			g.Options = append(g.Options, opt)
		}
		mine := s.skillCards(p, vals[pi], later[pi], base, g.Options, code, pct, skillTag, l)
		for _, m := range mine {
			seen[optFamily(m.Item.Name)] = true
		}
		for i := range g.Options {
			if p.enchant || p.item || !boostsSkill(c.Items[g.Options[i].Item.ID], code) {
				continue
			}
			g.Options[i].Tag = skillTag
		}
		// Enchants that only pay off at a higher refine or grade.
		var lk []int
		for k, r := range later[pi] {
			if r > 1.003 && p.pool[k].ID != p.current && !seen[optFamily(p.pool[k].Name.Get(l))] {
				lk = append(lk, k)
			}
		}
		sort.SliceStable(lk, func(a, z int) bool { return later[pi][lk[a]] > later[pi][lk[z]] })
		var laterOpts []optOption
		for _, k := range lk {
			fam := optFamily(p.pool[k].Name.Get(l))
			if seen[fam] || len(laterOpts) >= optLaterMax {
				continue
			}
			seen[fam] = true
			laterOpts = append(laterOpts, optOption{Item: viewItem(p.pool[k], l), Kind: kind, Gain: pct(vals[pi][k]), Later: true,
				Slot: g.Slot, Index: p.index, Tiers: s.optTiers(p, vals[pi], fam, pct, kind, l)})
		}
		// Skill enchants that don't touch the chosen attack: measure each on
		// the learned skill it boosts.
		if len(g.Options) == 0 && p.enchant && o.Goal == GoalDamage && !calc.IsCostume(g.Slot) {
			if heldID, held := s.optHeldSkill(b, p.current, l); heldID != 0 {
				if heldID != b.Atk.Skill { // for the attack itself it is simply the best
					g.SkillHeld = held
				}
			} else {
				g.Options = s.optSkillEnchants(b, p, l)
			}
		}
		g.Options = append(append(g.Options, mine...), laterOpts...)
		if len(g.Options) == 0 {
			// Keep the position listed, saying why nothing is suggested.
			g.Best = p.current != 0
			if !g.Best && p.enchant {
				g.SkillOnly = true
				for _, it := range p.pool {
					if !strings.Contains(it.Script, "bSkillAtk") {
						g.SkillOnly = false
						break
					}
				}
			}
		}
		res.Groups = append(res.Groups, g)
	}
	ctx2, cancel2 := context.WithTimeout(parent, extraTimeout)
	defer cancel2()
	if o.Costumes {
		opts, n := s.stoneSets(ctx2, b, o.Goal, l)
		res.Tested += n
		if len(opts) > 0 {
			res.Groups = append(res.Groups, optGroup{Kind: "set", Slot: "set", Label: tr(l, "Conjunto de visuais"), Options: opts})
		}
	}
	if o.Cards {
		tag := ""
		if code != "" {
			tag = tr(l, "conjunto de") + " " + c.Skills[b.Atk.Skill].Name.Get(l)
		}
		cardTag := ""
		if code != "" {
			cardTag = tr(l, "cartas de") + " " + c.Skills[b.Atk.Skill].Name.Get(l)
		}
		groups, m := s.multiCards(ctx2, b, pos, vals, later, o, code, cardTag, l)
		res.Groups = append(res.Groups, groups...)
		res.Tested += m
		opts, n, missing := s.cardSets(ctx2, b, o, tag, l)
		res.NoSlot = missing
		res.Tested += n
		if len(opts) > 0 {
			res.Groups = append(res.Groups, optGroup{Kind: "set", Slot: "cards", Label: tr(l, "Conjunto de cartas"), Options: opts})
		}
	}
	s.optDetails(ctx2, b, &res, o, pct, l)
	// Biggest gains first.
	sort.SliceStable(res.Groups, func(a, z int) bool {
		return gainOf(res.Groups[a]) > gainOf(res.Groups[z])
	})
	return res
}

// optSkillCode finds the skills an enchant boosts (bonus2 bSkillAtk,"CODE",n).
var optSkillCode = regexp.MustCompile(`bSkillAtk\s*,\s*"([A-Z0-9_]+)"`)

// optHeldSkill is the learned skill (ID, name) the enchant id boosts; 0 if none.
func (s *Server) optHeldSkill(b calc.Build, id int, l string) (int, string) {
	c := s.opt.Catalog
	it := c.Items[id]
	if it == nil {
		return 0, ""
	}
	for _, m := range optSkillCode.FindAllStringSubmatch(it.Script, -1) {
		for sid, lv := range b.Skills {
			if sk := c.Skills[sid]; sk != nil && lv > 0 && sk.Code == m[1] {
				return sid, sk.Name.Get(l)
			}
		}
	}
	return 0, ""
}

// optSkillEnchants ranks a position's skill enchants by the gain each gives
// to the learned skill it boosts (that skill as the attack, at its learned
// level), best first. Runs after the parallel pass; there are few of them.
func (s *Server) optSkillEnchants(b calc.Build, p optPosition, l string) []optOption {
	c := s.opt.Catalog
	learned := map[string]int{} // skill code -> ID, learned skills only
	for id, lv := range b.Skills {
		if sk := c.Skills[id]; sk != nil && lv > 0 {
			learned[sk.Code] = id
		}
	}
	type hit struct {
		opt  optOption
		gain float64
	}
	var hits []hit
	bases := map[int]float64{}
	for _, it := range p.pool {
		if it.ID == p.current {
			continue
		}
		for _, m := range optSkillCode.FindAllStringSubmatch(it.Script, -1) {
			id, ok := learned[m[1]]
			if !ok {
				continue
			}
			sb := b
			sb.Atk = calc.AttackChoice{Skill: id, Level: b.Skills[id]}
			if _, ok := bases[id]; !ok {
				bases[id] = s.optExpected(sb)
			}
			base := bases[id]
			sb.Equip = maps.Clone(b.Equip)
			for _, k := range p.slots {
				e := sb.Equip[k]
				e.Cards[p.index] = it.ID
				sb.Equip[k] = e
			}
			v := s.optExpected(sb)
			if base <= 0 || v <= base*1.0005 {
				continue
			}
			g := v / base
			hits = append(hits, hit{optOption{Item: viewItem(it, l), Kind: "enchant", Value: fmtInt(int64(v + 0.5)), Gain: pctOf(g, l),
				Skill: c.Skills[id].Name.Get(l), Slot: p.slots[0], Index: p.index, Positive: true}, g})
			break
		}
	}
	sort.SliceStable(hits, func(a, z int) bool { return hits[a].gain > hits[z].gain })
	var out []optOption
	seen := map[string]bool{}
	for _, h := range hits {
		if len(out) >= optTop {
			break
		}
		if fam := optFamily(h.opt.Item.Name); !seen[fam] {
			seen[fam] = true
			out = append(out, h.opt)
		}
	}
	return out
}

// pctOf formats a damage ratio as a gain ("+12,5%").
func pctOf(ratio float64, l string) string {
	txt := strconv.FormatFloat((ratio-1)*100, 'f', 1, 64)
	if l != "en" {
		txt = strings.Replace(txt, ".", ",", 1)
	}
	return "+" + txt + "%"
}

var optTier = regexp.MustCompile(`\s+(\+?\d+|[IVX]+|(?i:nv|lv)\.?\s*\d+)(\)?)$`)

// optFamily strips a trailing tier ("Insígnia do Talento 10" -> "Insígnia do Talento").
func optFamily(name string) string {
	return strings.ToLower(optTier.ReplaceAllString(strings.TrimSpace(name), "$2"))
}

func gainOf(g optGroup) float64 {
	if len(g.Options) == 0 || g.Options[0].Later {
		return 0
	}
	f, _ := strconv.ParseFloat(strings.NewReplacer("+", "", "%", "", ",", ".").Replace(g.Options[0].Gain), 64)
	return f
}
