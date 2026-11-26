package view

import (
	"maps"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"ragnarok-of-building/internal/calc"
)

// swapItem returns b with item id put in slot, as the equipment window would:
// a headgear takes every head slot it covers, a two-handed weapon drops the
// shield (and a shield drops a two-handed weapon), and refine, grade, cards
// and enchants carry over where the new item takes them. With card >= 0 it
// is a card/enchant put in that position of the slot's item instead.
func (s *Server) swapItem(b calc.Build, slot string, id, card int) calc.Build {
	c := s.opt.Catalog
	// Every map is copied: Normalize writes to them, and the picker swaps
	// many items in parallel from one build.
	nb := b
	nb.Equip = maps.Clone(b.Equip)
	if nb.Equip == nil { // a build with nothing equipped yet
		nb.Equip = map[string]calc.Equip{}
	}
	nb.Skills = maps.Clone(b.Skills)
	nb.Buffs = maps.Clone(b.Buffs)
	nb.Atk.States = maps.Clone(b.Atk.States)
	cur := b.Equip[slot]
	if card >= 0 && card < 4 {
		heads := []string{slot}
		if strings.HasPrefix(slot, "Head_") {
			for _, h := range []string{calc.HeadTop, calc.HeadMid, calc.HeadLow} {
				if h != slot && b.Equip[h].Item == cur.Item {
					heads = append(heads, h)
				}
			}
		}
		for _, h := range heads {
			e := nb.Equip[h]
			e.Cards[card] = id
			nb.Equip[h] = e
		}
		calc.Normalize(&nb, c)
		return nb
	}
	it := c.Items[id]
	if it == nil {
		return nb
	}
	e := calc.Equip{Item: id, Refine: cur.Refine, Grade: cur.Grade}
	if ci := c.Items[cur.Item]; ci != nil {
		for i, cd := range cur.Cards {
			if cd == 0 {
				continue
			}
			switch {
			case i < ci.Slots && i < it.Slots: // card positions of both
				e.Cards[i] = cd
			case i >= it.Slots && slices.Contains(c.Enchants[id][i], cd):
				e.Cards[i] = cd
			}
		}
	}
	// The old item leaves every slot it held; the new one takes its slots.
	targets := []string{slot}
	if strings.HasPrefix(slot, "Head_") {
		for k, v := range b.Equip {
			if strings.HasPrefix(k, "Head_") && v.Item == cur.Item && cur.Item != 0 {
				delete(nb.Equip, k)
			}
		}
		targets = nil
		for _, h := range []string{calc.HeadTop, calc.HeadMid, calc.HeadLow} {
			if it.HasLocation(h) {
				targets = append(targets, h)
			}
		}
		if len(targets) == 0 {
			targets = []string{slot}
		}
		for _, h := range targets { // anything left in a slot the new one covers goes
			if old := nb.Equip[h]; old.Item != 0 {
				for k, v := range nb.Equip {
					if strings.HasPrefix(k, "Head_") && v.Item == old.Item {
						delete(nb.Equip, k)
					}
				}
			}
		}
	}
	for _, t := range targets {
		nb.Equip[t] = e
	}
	calc.Normalize(&nb, c)
	return nb
}

// statLine is one row of an item comparison: what the swap changes.
type statLine struct {
	Label, Old, New, Diff string
	Better, Same          bool // Same: no meaningful change (under 0.05%)
}

type swapView struct {
	Current  string // the item (or card) being replaced, "" when the slot is empty
	Target   string
	Expected *statLine
	Lines    []statLine
}

// swapCompare computes the build before and after the swap.
func (s *Server) swapCompare(b, nb calc.Build, cur string, l string) swapView {
	c := s.opt.Catalog
	r0, r1 := calc.Compute(b, c), calc.Compute(nb, c)
	v := swapView{Current: cur}
	if m := c.Monster(b.Target); m != nil {
		e0 := s.damageView(b, r0, l).Dmg.Expected
		e1 := s.damageView(nb, r1, l).Dmg.Expected
		v.Target = m.NameIn(l)
		if e0 > 0 {
			ratio := e1 / e0
			ln := statLine{Label: tr(l, "Dano esperado"), Old: fmtInt(int64(e0 + 0.5)), New: fmtInt(int64(e1 + 0.5)),
				Diff: pctChange(ratio, l), Better: ratio > 1.0005, Same: ratio > 0.9995 && ratio < 1.0005}
			if ln.Same {
				ln.Diff = "="
			}
			v.Expected = &ln
		}
	}
	add := func(label string, a, z int) {
		if a == z {
			return
		}
		d := z - a
		sign := "+"
		if d < 0 {
			sign = ""
		}
		v.Lines = append(v.Lines, statLine{Label: tr(l, label), Old: fmtInt(int64(a)), New: fmtInt(int64(z)),
			Diff: sign + fmtInt(int64(d)), Better: d > 0})
	}
	add("ATQ", r0.StatusATK+r0.EquipATK, r1.StatusATK+r1.EquipATK)
	add("ATQM", r0.MATKMax+r0.EquipMATK, r1.MATKMax+r1.EquipMATK)
	add("P.ATQ", r0.PATK, r1.PATK)
	add("S.ATQM", r0.SMATK, r1.SMATK)
	add("Precisão", r0.HIT, r1.HIT)
	add("Crítico", int(r0.CRIT), int(r1.CRIT))
	add("C.RATE", r0.CRATE, r1.CRATE)
	add("ASPD", r0.ASPD, r1.ASPD)
	add("Esquiva", r0.FLEE, r1.FLEE)
	add("DEF", r0.HardDEF+r0.SoftDEF, r1.HardDEF+r1.SoftDEF)
	add("DEFM", r0.HardMDEF+r0.SoftMDEF, r1.HardMDEF+r1.SoftMDEF)
	add("RES", r0.RES, r1.RES)
	add("MRES", r0.MRES, r1.MRES)
	add("HP", r0.HP, r1.HP)
	add("SP", r0.SP, r1.SP)
	for i, n := range statNames {
		add(n, r0.Stats[i].Base+r0.Stats[i].Bonus, r1.Stats[i].Base+r1.Stats[i].Bonus)
	}
	for i, n := range traitNames {
		add(n, r0.Traits[i].Base+r0.Traits[i].Bonus, r1.Traits[i].Base+r1.Traits[i].Bonus)
	}
	return v
}

// pctChange formats a ratio as a signed % change ("+12,4%", "-3,1%").
func pctChange(ratio float64, l string) string {
	g := (ratio - 1) * 100
	txt := strconv.FormatFloat(g, 'f', 1, 64)
	if l != "en" {
		txt = strings.Replace(txt, ".", ",", 1)
	}
	if g >= 0 {
		txt = "+" + txt
	}
	return txt + "%"
}

// pickGains is the expected damage change of each picker result against the
// build's target ("" when unknown), computed in parallel.
func (s *Server) pickGains(b calc.Build, slot string, items []itemView, card int, l string) map[int]statLine {
	c := s.opt.Catalog
	if c.Monster(b.Target) == nil {
		return nil
	}
	base := s.damageView(b, calc.Compute(b, c), l).Dmg.Expected
	if base <= 0 {
		return nil
	}
	out := make(map[int]statLine, len(items))
	var mu sync.Mutex
	var wg sync.WaitGroup
	ch := make(chan int)
	for w := 0; w < max(1, runtime.NumCPU()-1); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ch {
				yield()
				nb := s.swapItem(b, slot, id, card)
				e := s.damageView(nb, calc.Compute(nb, c), l).Dmg.Expected
				ratio := e / base
				ln := statLine{Diff: pctChange(ratio, l), Better: ratio > 1.0005, Same: ratio > 0.9995 && ratio < 1.0005}
				if ln.Same {
					ln.Diff = "="
				}
				mu.Lock()
				out[id] = ln
				mu.Unlock()
			}
		}()
	}
	for _, it := range items {
		ch <- it.ID
	}
	close(ch)
	wg.Wait()
	return out
}
