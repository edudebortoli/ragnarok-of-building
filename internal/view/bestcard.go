package view

import (
	"context"
	"maps"
	"runtime"
	"sort"
	"sync"

	"ragnarok-of-building/internal/calc"
)

// Best card for a card or enchant position: every card that fits (the
// client's enchant list for enchant positions), ranked by the goal —
// expected damage against the target (GoalDamage) or effective HP against
// it (GoalPhysDef, GoalMagDef) — like the optimizer.

// BestCards ranks the cards for position idx of the item in slot.
func (v *Views) BestCards(ctx context.Context, b calc.Build, slot string, idx int, goal string, l string) BestResult {
	return v.BestCardsTraits(ctx, b, slot, idx, goal, false, l)
}

// BestCardsTraits is BestCards; with traits, the enchants that depend on a
// trait are also ranked with the traits changed for them.
func (v *Views) BestCardsTraits(ctx context.Context, b calc.Build, slot string, idx int, goal string, traits bool, l string) BestResult {
	s := v.s
	c := s.opt.Catalog
	var res BestResult
	if c.Monster(b.Target) == nil {
		res.NoTarget = true
		return res
	}
	b.Equip, b.Skills, b.Buffs = maps.Clone(b.Equip), maps.Clone(b.Skills), maps.Clone(b.Buffs)
	b.Atk.States = maps.Clone(b.Atk.States)
	if b.Equip == nil {
		b.Equip = map[string]calc.Equip{}
	}
	calc.Normalize(&b, c)
	host := c.Items[b.Equip[slot].Item]
	if host == nil || idx < 0 || idx > 3 {
		return res
	}
	// Against the build as it is, like Melhor item (the current card reads "=").
	base := s.optScore(b, goal)
	if base <= 0 {
		res.NoTarget = true
		return res
	}
	var ids []int
	if ench, ok := c.Enchants[host.ID]; ok && idx >= host.Slots {
		for _, id := range ench[idx] {
			if !calc.EnchantBlocked(host.ID, idx, b.Equip[slot].Cards, id) {
				ids = append(ids, id)
			}
		}
	} else {
		for _, it := range c.Items {
			if it.Type == "Card" && calc.CardFits(it, slot) && it.Name.Get(l) != "" &&
				(idx < host.Slots) == (it.SubType != "Enchant") {
				ids = append(ids, it.ID)
			}
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
				nb := s.swapItem(b, slot, id, idx)
				if nb.Equip[slot].Cards[idx] != id { // Normalize dropped it
					continue
				}
				e := s.optScore(nb, goal)
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
	// The cards that complete a set with cards not worn yet: the set placed
	// as a whole, as another row.
	type comboScored struct {
		id    int
		ratio float64
		parts []ComboPart
	}
	var combos []comboScored
	for _, id := range ids {
		if ctx.Err() != nil {
			res.Partial = true
			break
		}
		for _, parts := range s.comboPlacements(b, slot, idx, id, goal) {
			nb := s.applyCombo(b, slot, idx, id, parts)
			if e := s.optScore(nb, goal); e > 0 {
				combos = append(combos, comboScored{id, e / base, parts})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ratio != out[j].ratio {
			return out[i].ratio > out[j].ratio
		}
		return out[i].id < out[j].id
	})
	for _, sc := range out[:min(len(out), bestTop)] {
		ln := StatLine{Diff: pctChange(sc.ratio, l), Better: sc.ratio > 1.0005, Same: sc.ratio > 0.9995 && sc.ratio < 1.0005}
		if ln.Same {
			ln.Diff = "="
		}
		res.Items = append(res.Items, BestItem{Item: viewItem(c.Items[sc.id], l), Gain: ln, Ratio: sc.ratio})
	}
	// The sets that beat the single cards' ranking enter it, best first.
	sort.Slice(combos, func(i, j int) bool { return combos[i].ratio > combos[j].ratio })
	for _, cs := range combos[:min(len(combos), 5)] {
		if cs.ratio <= 1.0005 {
			continue
		}
		parts := cs.parts
		for i := range parts {
			parts[i].CardName = c.Items[parts[i].Card].Name.Get(l)
			parts[i].SlotLabel = tr(l, slotLabel(parts[i].Slot))
		}
		bi := BestItem{Item: viewItem(c.Items[cs.id], l), Ratio: cs.ratio, Combo: parts,
			Gain: StatLine{Diff: pctChange(cs.ratio, l), Better: true}}
		insertBest(&res, bi)
	}
	if traits {
		for _, bi := range s.traitAlternatives(ctx, b, slot, idx, ids, goal, base, l) {
			insertBest(&res, bi)
		}
	}
	return res
}

// insertBest keeps the ranking in order.
func insertBest(res *BestResult, bi BestItem) {
	at := sort.Search(len(res.Items), func(i int) bool { return res.Items[i].Ratio < bi.Ratio })
	res.Items = append(res.Items[:at], append([]BestItem{bi}, res.Items[at:]...)...)
}

// comboPlacements: for each set of card id whose other pieces are all
// cards not worn yet, where they go — each in the free or replaced card
// position (of another worn item) that scores best, one after the other.
func (s *Server) comboPlacements(b calc.Build, slot string, idx, id int, goal string) [][]ComboPart {
	c := s.opt.Catalog
	worn := map[int]int{}
	for _, e := range b.Equip {
		for _, cd := range e.Cards {
			if cd > 0 {
				worn[cd]++
			}
		}
	}
	var out [][]ComboPart
	for _, ci := range c.CombosWith(id) {
		cb := c.Combos[ci]
		var need []int
		self := false
		ok := true
		for _, p := range cb.Items {
			if p == id && !self {
				self = true
				continue
			}
			it := c.Items[p]
			if it == nil || it.Type != "Card" || it.SubType == "Enchant" && !isStone(it) {
				ok = false // a set with an item (not a card): out of scope
				break
			}
			need = append(need, p)
		}
		if !ok || len(need) == 0 || len(need) > 3 {
			continue
		}
		// Pieces already worn need no placing.
		left := maps.Clone(worn)
		var place []int
		for _, p := range need {
			if left[p] > 0 {
				left[p]--
				continue
			}
			place = append(place, p)
		}
		if len(place) == 0 {
			continue // the set is complete already: the single row counts it
		}
		used := map[[2]any]bool{{slot, idx}: true}
		cur := s.swapItem(b, slot, id, idx)
		var parts []ComboPart
		for _, p := range place {
			best, bestScore := ComboPart{}, -1.0
			for sl, e := range cur.Equip {
				host := c.Items[e.Item]
				if host == nil || !calc.CardFits(c.Items[p], sl) {
					continue
				}
				from, to := 0, min(host.Slots, 4)
				if calc.IsCostume(sl) { // its one stone
					from = stoneIndex(e)
					to = from + 1
				}
				for k := from; k < to; k++ {
					if used[[2]any{sl, k}] {
						continue
					}
					nb := s.swapItem(cur, sl, p, k)
					if nb.Equip[sl].Cards[k] != p {
						continue
					}
					if sc := s.optScore(nb, goal); sc > bestScore {
						best, bestScore = ComboPart{Slot: sl, Idx: k, Card: p}, sc
					}
				}
			}
			if bestScore < 0 {
				parts = nil
				break
			}
			used[[2]any{best.Slot, best.Idx}] = true
			cur = s.swapItem(cur, best.Slot, best.Card, best.Idx)
			parts = append(parts, best)
		}
		if parts != nil {
			out = append(out, parts)
		}
	}
	return out
}

// applyCombo puts card id at idx of slot and the set's other cards where
// parts says.
func (s *Server) applyCombo(b calc.Build, slot string, idx, id int, parts []ComboPart) calc.Build {
	nb := s.swapItem(b, slot, id, idx)
	for _, p := range parts {
		nb = s.swapItem(nb, p.Slot, p.Card, p.Idx)
	}
	return nb
}

// ApplyCombo is applyCombo for the UI (a Melhor carta set row).
func (v *Views) ApplyCombo(b calc.Build, slot string, idx, id int, parts []ComboPart) calc.Build {
	return v.s.applyCombo(b, slot, idx, id, parts)
}
