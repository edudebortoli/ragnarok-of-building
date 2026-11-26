package ui

import (
	"fmt"
	"image"
	"strconv"

	"gioui.org/layout"
	"gioui.org/unit"

	"ragnarok-of-building/internal/view"
)

// ---- Replay import and its damage recap (win-rdmg) ----

const maxReplay = 16 << 20

type replayState struct {
	dmg  *view.ReplayDamage
	who  Select
	char int
	mobs map[int]*Details
}

// importReplay asks for a .rrf and opens the build it shows.
func (a *App) importReplay() {
	a.openFile(a.T("Abrir um replay (.rrf) do jogo"), "Replays (*.rrf)|*.rrf", a.importReplayFile)
}

func (a *App) importReplayFile(buf []byte) {
	if len(buf) > maxReplay {
		a.Say(a.T("Arquivo grande demais (máx. 16 MB)."), true)
		return
	}
	a.Say(a.T("Lendo o replay…"), false)
	go func() {
		b, notes, dmg, err := a.V.ImportReplay(buf, a.Lang)
		if err != nil {
			a.Say(a.T("Não consegui ler este replay. Ele é um .rrf gravado pelo cliente do RO LATAM?"), true)
		} else {
			for _, n := range notes {
				a.Say(n, false)
			}
			a.mu.Lock()
			a.replay = replayState{dmg: dmg}
			a.savedID = ""
			a.mu.Unlock()
			a.OpenBuild(b)
			if dmg != nil {
				a.showReplayDamage = true
			}
		}
		if a.Invalidate != nil {
			a.Invalidate()
		}
	}()
}

func (a *App) replayDamage() *view.ReplayDamage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.replay.dmg
}

func (a *App) replayBody(gtx C) D {
	th, s := a.Th, &a.replay
	dv := a.replayDamage()
	if dv == nil || len(dv.Chars) == 0 {
		return T(th, a.T("Importe um replay com dano para ver o resumo.")).C(ColMuted).Layout(gtx)
	}
	s.char = min(s.char, len(dv.Chars)-1)
	c := dv.Chars[s.char]
	who := func(gtx C) D {
		var extra []layout.Widget
		if len(dv.Chars) > 1 {
			var opts []Option
			for i, ch := range dv.Chars {
				l := ch.Name
				if ch.Job != "" {
					l += " (" + ch.Job + ")"
				}
				opts = append(opts, Option{strconv.Itoa(i), l + " · " + ch.Total})
			}
			extra = append(extra, func(gtx C) D {
				d, val, ok := s.who.Layout(gtx, th, a.Desk, opts, strconv.Itoa(s.char), 260)
				if ok {
					s.char, _ = strconv.Atoi(val)
				}
				return d
			})
		} else {
			name := c.Name
			if c.Job != "" {
				name += " (" + c.Job + ")"
			}
			extra = append(extra, B(th, name).W())
		}
		if dv.Recorded != "" {
			extra = append(extra, T(th, "· "+dv.Recorded).W())
		}
		if dv.Duration != "" {
			extra = append(extra, T(th, "· "+dv.Duration).W())
		}
		return Row(4, extra...)(gtx)
	}
	total := func(label, v string) layout.Widget {
		return func(gtx C) D {
			return Box{Bg: ColPanel, Line: ColListBorder, Radius: 3, Pad: Inset(3, 8, 3, 8)}.Layout(gtx,
				Col(0, T(th, a.T(label)).Sz(SmallText).C(ColMuted).W(), B(th, v).Sz(13).W()))
		}
	}
	totals := []layout.Widget{total("Dano total", c.Total)}
	if c.DPS != "" {
		totals = append(totals, total("Dano por segundo", c.DPS))
	}
	totals = append(totals, total("Golpes", strconv.Itoa(c.Hits)), total("Abates", strconv.Itoa(c.Kills)))

	if s.mobs == nil {
		s.mobs = map[int]*Details{}
	}
	kids := []layout.Widget{who, Row(6, totals...),
		Row(4, B(th, a.T("Habilidades")).C(ColTitleText).W(), T(th, a.T("(do maior dano para o menor)")).Sz(SmallText).C(ColMuted).W()),
		a.rdmgTable(c.Skills),
		Row(4, B(th, a.T("Monstros")).C(ColTitleText).W(), T(th, a.T("(abates: mortos / atingidos)")).Sz(SmallText).C(ColMuted).W()),
	}
	for _, m := range c.Monsters {
		m := m
		det := s.mobs[m.ID]
		if det == nil {
			det = &Details{}
			s.mobs[m.ID] = det
		}
		title := fmt.Sprintf("%s · %s · %s · %s %d/%d · %d %s", m.Name, m.Total, m.PctS, a.T("Abates"), m.Kills, m.Count, m.Hits, a.T("golpes"))
		kids = append(kids, func(gtx C) D { return det.Layout(gtx, th, title, false, a.rdmgTable(m.Skills)) })
	}
	kids = append(kids, T(th, a.T("Dano que cada personagem do replay causou em monstros (escolha o personagem no topo). Golpes de habilidades com vários impactos (meteoros) contam um a um. O jogo não marca críticos no replay.")).Sz(SmallText).C(ColMuted).Wrapping().W())
	return Col(6, kids...)(gtx)
}

// rdmgTable is a skill table with a % bar.
func (a *App) rdmgTable(skills []view.ReplaySkill) layout.Widget {
	th := a.Th
	cols := []struct {
		w     unit.Dp
		title string
	}{{0, "Habilidade"}, {76, "Dano"}, {110, "%"}, {48, "Golpes"}, {64, "Média"}, {64, "Máximo"}}
	cell := func(i int, w layout.Widget) layout.FlexChild {
		if cols[i].w == 0 {
			return layout.Flexed(1, w)
		}
		return layout.Rigid(Fixed(cols[i].w, w))
	}
	return func(gtx C) D {
		var hdr []layout.FlexChild
		for i, c := range cols {
			hdr = append(hdr, cell(i, B(th, a.T(c.title)).Sz(SmallText).C(ColLabel).W()))
		}
		rows := []layout.Widget{func(gtx C) D { return layout.Flex{}.Layout(gtx, hdr...) }}
		for _, s := range skills {
			s := s
			rows = append(rows, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					cell(0, Row(4, func(gtx C) D {
						if s.ID == 0 {
							return D{Size: image.Pt(gtx.Dp(20), gtx.Dp(20))}
						}
						return a.Icons.Icon(gtx, view.SkillIcon(s.ID), 20)
					}, T(th, s.Name).W())),
					cell(1, T(th, s.Total).W()),
					cell(2, Row(4, func(gtx C) D { return pctBar(gtx, s.Pct) }, T(th, s.PctS).Sz(SmallText).W())),
					cell(3, T(th, strconv.Itoa(s.Hits)).W()),
					cell(4, T(th, s.Avg).W()),
					cell(5, T(th, s.Max).W()),
				)
			})
		}
		return Col(2, rows...)(gtx)
	}
}

// pctBar is a small horizontal bar filled to pct.
func pctBar(gtx C, pct float64) D {
	w, h := gtx.Dp(50), gtx.Dp(8)
	r := image.Rectangle{Max: image.Pt(w, h)}
	Fill(gtx, r, rgb(0xe4e6ea))
	fw := int(float64(w) * min(max(pct, 0), 100) / 100)
	if fw > 0 {
		Fill(gtx, image.Rect(0, 0, fw, h), rgb(0x7f9fd9))
	}
	Border(gtx, r, ColListBorder, 1, 0)
	return D{Size: r.Max}
}
