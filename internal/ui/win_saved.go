package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"

	"ragnarok-of-building/internal/view"
)

// ---- Modal dialogs (the web version's prompt and confirm) ----

// Modal is a centered RO window over a dimmed desk: a message, an optional
// text box and its buttons.
type Modal struct {
	Title, Text string
	Input       *Input // nil: no text box
	Buttons     []string
	OnClose     func(button int, text string) // -1 = cancelled (Esc, click outside)

	btns  []widget.Clickable
	catch bool
	win   Window
}

// ShowModal opens a dialog (replacing any other).
func (a *App) ShowModal(m *Modal) {
	m.btns = make([]widget.Clickable, len(m.Buttons))
	a.modal = m
}

// Ask asks for a text (prompt).
func (a *App) Ask(title, text, value string, done func(string)) {
	in := &Input{}
	in.Ed.SetText(value)
	in.Ed.SetCaret(len([]rune(value)), 0)
	a.ShowModal(&Modal{Title: title, Text: text, Input: in, Buttons: []string{a.T("OK"), a.T("Cancelar")},
		OnClose: func(b int, s string) {
			if b == 0 {
				done(s)
			}
		}})
}

// Confirm asks Sim/Não.
func (a *App) Confirm(text string, yes func()) {
	a.ShowModal(&Modal{Title: "Ragnarok of Building", Text: text, Buttons: []string{a.T("Sim"), a.T("Não")},
		OnClose: func(b int, _ string) {
			if b == 0 {
				yes()
			}
		}})
}

func (a *App) layoutModal(gtx C) {
	m := a.modal
	if m == nil {
		return
	}
	th := a.Th
	size := gtx.Constraints.Max
	closeWith := func(b int) {
		a.modal = nil
		text := ""
		if m.Input != nil {
			text = m.Input.Text()
		}
		if m.OnClose != nil {
			m.OnClose(b, text)
		}
	}
	for i := range m.btns {
		if m.btns[i].Clicked(gtx) {
			closeWith(i)
			return
		}
	}
	// Dim the desk; clicks there do nothing.
	Fill(gtx, image.Rectangle{Max: size}, color.NRGBA{A: 0x40})
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &m.catch)
	area.Pop()
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: &m.catch, Kinds: pointer.Press}); !ok {
			break
		}
	}
	m.win.Title = m.Title
	m.win.Width = 360
	m.win.Body = func(gtx C) D {
		kids := []layout.Widget{T(th, m.Text).Wrapping().W()}
		if m.Input != nil {
			kids = append(kids, func(gtx C) D {
				gtx.Execute(keyFocus(&m.Input.Ed))
				d, _, sub := m.Input.Layout(gtx, th, 0)
				if sub {
					defer closeWith(0)
				}
				return d
			})
		}
		btns := make([]layout.Widget, len(m.Buttons))
		for i, b := range m.Buttons {
			i, b := i, b
			btns[i] = func(gtx C) D { return Button{Label: b, MinWidth: 60, Bold: i == 0}.Layout(gtx, th, &m.btns[i]) }
		}
		kids = append(kids, func(gtx C) D {
			return layout.E.Layout(gtx, Row(4, btns...))
		})
		return Col(8, kids...)(gtx)
	}
	rec := op.Record(gtx.Ops)
	g := gtx
	g.Constraints = layout.Constraints{Max: image.Pt(min(gtx.Dp(360), size.X), size.Y)}
	d := m.win.layout(g, th, NewDesk(), false)
	call := rec.Stop()
	st := op.Offset(image.Pt((size.X-d.Size.X)/2, (size.Y-d.Size.Y)/3)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	st.Pop()
	if m.win.close.Clicked(gtx) {
		closeWith(-1)
	}
}

// ---- Saving, new build, clipboard ----

func (a *App) loadSaved() []view.SavedBuild { return view.LoadSaved(a.Store) }

func (a *App) currentSaved() *view.SavedBuild {
	for _, e := range a.loadSaved() {
		if e.ID == a.savedID {
			return &e
		}
	}
	return nil
}

// saveCurrent saves the build, asking its name (Salvar; Ctrl+S asks only
// the first time).
func (a *App) saveCurrent() { a.saveBuild(true) }

func (a *App) saveBuild(ask bool) {
	cur := a.currentSaved()
	save := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			name = a.T("Sem nome")
		}
		if len([]rune(name)) > 40 {
			name = string([]rune(name)[:40])
		}
		b := a.Build()
		v := a.View()
		e := view.SavedBuild{ID: a.savedID, Name: name, Code: view.EncodeBuild(b), JobID: b.Job, Job: v.JobName,
			Base: b.BaseLv, JobLv: b.JobLv, At: time.Now().UnixMilli()}
		if e.ID == "" || cur == nil {
			e.ID = strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		list := a.loadSaved()
		found := false
		for i := range list {
			if list[i].ID == e.ID {
				list[i], found = e, true
			}
		}
		if !found {
			list = append(list, e)
		}
		if err := view.WriteSaved(a.Store, list); err != nil {
			a.Say(a.T("Não consegui gravar os builds.")+" "+err.Error(), true)
			return
		}
		a.savedID = e.ID
		a.needSave = true
		a.Say(fmt.Sprintf(a.T(`Build "%s" salvo.`), e.Name), false)
	}
	if !ask && cur != nil {
		save(cur.Name)
		return
	}
	name := ""
	if cur != nil {
		name = cur.Name
	}
	a.Ask(a.T("Salvar"), a.T("Nome do build:"), name, save)
}

// newBuild starts over from a class (the class list opens).
func (a *App) newBuild() {
	a.Confirm(a.T("Começar um build novo? Salve o atual antes (Ctrl+S) se quiser guardá-lo."), func() {
		a.savedID = ""
		a.basic.newMode = true
		a.Desk.OpenPopup(&a.basic.classDD)
		if !a.wBasic.Open {
			a.Desk.Toggle(a.wBasic)
		}
	})
}

// copyText puts text on the clipboard and says so.
func (a *App) copyText(gtx C, text, done string) {
	gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(text))})
	a.Say(done, false)
}

// openCode opens a build code (or a link with ?b=), as a new build unless
// keep is true.
func (a *App) openCode(code string, keep bool) bool {
	b, ok := view.DecodeBuild(code)
	if !ok {
		return false
	}
	if !keep {
		a.savedID = ""
	}
	a.OpenBuild(b)
	return true
}

// codeInLink finds the code in a share link: ?b= (older links) or #b= (the
// web page's).
var codeInLink = regexp.MustCompile(`[?&#]b=([A-Za-z0-9_-]+)`)

// OpenShared opens a build code that came with the page's link, on the
// window's goroutine (safe to call from anywhere).
func (a *App) OpenShared(code string) {
	a.later(func() {
		if !a.openCode(code, false) {
			a.Say(a.T("Não consegui abrir o build do link."), true)
		}
	})
}

// ---- Builds salvos (win-saved) ----

type savedState struct {
	open, del []widget.Clickable
	list      widget.List
	link      Input
	openLink  widget.Clickable
	export    widget.Clickable
	imp       widget.Clickable
}

func (a *App) savedBody(gtx C) D {
	th, s := a.Th, &a.saved
	list := a.loadSaved()
	sortSaved(list)
	if len(s.open) < len(list) {
		s.open = make([]widget.Clickable, len(list))
		s.del = make([]widget.Clickable, len(list))
	}
	for i, e := range list {
		e := e
		if s.del[i].Clicked(gtx) {
			a.Confirm(fmt.Sprintf(a.T(`Excluir o build "%s"?`), e.Name), func() {
				var keep []view.SavedBuild
				for _, x := range a.loadSaved() {
					if x.ID != e.ID {
						keep = append(keep, x)
					}
				}
				view.WriteSaved(a.Store, keep)
				if a.savedID == e.ID {
					a.savedID = ""
				}
			})
		} else if s.open[i].Clicked(gtx) {
			if a.openCode(e.Code, false) {
				a.savedID = e.ID
			}
		}
	}
	if s.openLink.Clicked(gtx) {
		a.importText(s.link.Text(), false)
		s.link.Ed.SetText("")
	}
	if s.export.Clicked(gtx) {
		a.exportSaved()
	}
	if s.imp.Clicked(gtx) {
		a.importSaved()
	}
	var kids []layout.Widget
	if len(list) == 0 {
		kids = append(kids, T(th, a.T("Nenhum build salvo ainda. Aperte Salvar (ou Ctrl+S) para guardar o atual.")).C(ColMuted).Wrapping().W())
	} else {
		kids = append(kids, func(gtx C) D {
			gtx.Constraints.Max.Y = gtx.Dp(260)
			return Box{Bg: ColPanel, Line: ColListBorder, Pad: Inset(2, 2, 2, 2)}.Layout(gtx, func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				s.list.Axis = layout.Vertical
				return s.list.Layout(gtx, len(list), func(gtx C, i int) D {
					e := list[i]
					cl := &s.open[i]
					return cl.Layout(gtx, func(gtx C) D {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						m := op.Record(gtx.Ops)
						d := Inset(2, 6, 2, 6).Layout(gtx, func(gtx C) D {
							info := fmt.Sprintf("%s · %s %d/%d · %s", e.Job, a.T("Nv."), e.Base, e.JobLv,
								time.UnixMilli(e.At).Format("02/01/2006 15:04"))
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Flexed(1, Col(0, B(th, e.Name).W(), T(th, info).Sz(SmallText).C(rgb(0x5a6a88)).W())),
								layout.Rigid(func(gtx C) D { return Button{Label: "×", MinWidth: 18}.Layout(gtx, th, &s.del[i]) }),
							)
						})
						call := m.Stop()
						switch {
						case e.ID == a.savedID:
							Fill(gtx, image.Rectangle{Max: d.Size}, rgb(0xfff4cf))
						case cl.Hovered():
							Fill(gtx, image.Rectangle{Max: d.Size}, ColHover)
						}
						call.Add(gtx.Ops)
						pointerCursor(gtx, d.Size)
						return d
					})
				})
			})
		})
	}
	kids = append(kids,
		Row(4,
			T(th, a.T("Salvos no arquivo builds.json, na pasta do programa.")).Sz(SmallText).C(ColMuted).W(),
		),
		Row(4,
			func(gtx C) D { return Button{Label: a.T("Exportar")}.Layout(gtx, th, &s.export) },
			func(gtx C) D { return Button{Label: a.T("Importar")}.Layout(gtx, th, &s.imp) },
		),
		func(gtx C) D {
			s.link.Hint = a.T("Cole o link de um build compartilhado")
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					d, _, sub := s.link.Layout(gtx, th, 0)
					if sub {
						a.importText(s.link.Text(), false)
						s.link.Ed.SetText("")
					}
					return d
				}),
				layout.Rigid(Gap(4, 0)),
				layout.Rigid(func(gtx C) D { return Button{Label: a.T("Abrir")}.Layout(gtx, th, &s.openLink) }),
			)
		},
	)
	return Col(6, kids...)(gtx)
}

func sortSaved(list []view.SavedBuild) {
	for i := 1; i < len(list); i++ {
		for k := i; k > 0 && list[k].At > list[k-1].At; k-- {
			list[k], list[k-1] = list[k-1], list[k]
		}
	}
}

// exportSaved hands the player a backup file with every saved build (next
// to the program on the desktop).
func (a *App) exportSaved() {
	if a.SaveFile == nil {
		return
	}
	list := a.loadSaved()
	b, _ := json.MarshalIndent(map[string]any{"app": "ragnarok-of-building", "version": 1, "builds": list}, "", " ")
	name := "builds-ragnarok-of-building-" + time.Now().Format("2006-01-02") + ".json"
	where, err := a.SaveFile(name, b)
	if err != nil {
		a.Say(err.Error(), true)
		return
	}
	a.Say(fmt.Sprintf(a.T("%d build(s) exportado(s) para %s"), len(list), where), false)
}

// importSaved merges a backup file (the export, or a plain list): a build
// with an id already here is replaced when the file's is newer.
func (a *App) importSaved() {
	a.openFile(a.T("Importar builds"), "JSON (*.json)|*.json", a.importSavedFile)
}

// importSavedFile merges a backup's contents.
func (a *App) importSavedFile(raw []byte) {
	if len(raw) > maxReplay { // the hosts read at most one byte more
		a.Say(a.T("Arquivo grande demais (máx. 16 MB)."), true)
		return
	}
	var wrapped struct {
		Builds []view.SavedBuild `json:"builds"`
	}
	var incoming []view.SavedBuild
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Builds != nil {
		incoming = wrapped.Builds
	} else if json.Unmarshal(raw, &incoming) != nil {
		a.Say(a.T("Este arquivo não é um backup de builds."), true)
		return
	}
	list := a.loadSaved()
	n := 0
	for _, e := range incoming {
		if e.ID == "" || e.Code == "" {
			continue
		}
		i := -1
		for k := range list {
			if list[k].ID == e.ID {
				i = k
			}
		}
		switch {
		case i < 0:
			list = append(list, e)
			n++
		case e.At > list[i].At:
			list[i] = e
			n++
		}
	}
	if err := view.WriteSaved(a.Store, list); err != nil {
		a.Say(err.Error(), true)
		return
	}
	a.Say(fmt.Sprintf(a.T("%s build(s) importado(s)."), strconv.Itoa(n)), false)
}

// importText opens a pasted link, paste.rs link or code.
func (a *App) importText(v string, keep bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		a.Say(a.T("Cole um link ou código de build."), true)
		return
	}
	if m := codeInLink.FindStringSubmatch(v); m != nil {
		v = m[1]
	} else if strings.HasPrefix(strings.ToLower(v), "http") || strings.HasPrefix(strings.ToLower(v), "paste.rs/") {
		if a.NoPaste {
			a.Say(a.T("Links do paste.rs não abrem no navegador: peça o link ou o código do build."), true)
			return
		}
		a.Say(a.T("Lendo o paste…"), false)
		go func() {
			code, err := a.V.ReadPaste(context.Background(), v)
			if err != nil {
				a.Say(a.T("Não deu para ler o paste: ")+err.Error(), true)
			} else if !a.openCode(code, keep) {
				a.Say(a.T("Cole um link ou código de build."), true)
			}
			if a.Invalidate != nil {
				a.Invalidate()
			}
		}()
		return
	}
	if !a.openCode(regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(v, ""), keep) {
		a.Say(a.T("Cole um link ou código de build."), true)
	}
}

// ---- Compartilhar Build (win-share) ----

type shareState struct {
	copy, goBtn, imp widget.Clickable
	as, to           Select
	asVal, toVal     string
	code             Input
	importBox        Input
	pasted           string // paste link of the build code it was made for
	pastedFor        string
	busy             bool
}

func (a *App) shareText() string {
	v := a.View()
	b := v.Build
	names := [6]string{"STR", "AGI", "VIT", "INT", "DEX", "LUK"}
	traits := [6]string{"POW", "STA", "WIS", "SPL", "CON", "CRT"}
	var st []string
	for i, n := range names {
		st = append(st, fmt.Sprintf("%s %d", n, b.Stats[i]))
	}
	lines := []string{fmt.Sprintf("**%s** · %s %d/%d", v.JobName, a.T("Nv."), b.BaseLv, b.JobLv), strings.Join(st, " · ")}
	if b.Traits != [6]int{} {
		var tr []string
		for i, n := range traits {
			tr = append(tr, fmt.Sprintf("%s %d", n, b.Traits[i]))
		}
		lines = append(lines, strings.Join(tr, " · "))
	}
	var gear []string
	for _, list := range [][]view.SlotView{v.Slots, v.Shadow, v.Costume} {
		for _, s := range list {
			if s.Item == nil {
				continue
			}
			gear = append(gear, "- "+a.T(s.Label)+": "+itemTitle(s.Item, s.Refine, s.Grade))
		}
	}
	if len(gear) > 0 {
		lines = append(append(lines, ""), gear...)
	}
	return strings.Join(append(lines, "", v.Code), "\n")
}

func (a *App) shareBody(gtx C) D {
	th, s := a.Th, &a.share
	v := a.View()
	if s.asVal == "" {
		s.asVal, s.toVal = "paste", "new"
		if a.ShareURL != nil {
			s.asVal = "link"
		}
	}
	shown := v.Code
	if s.pastedFor == v.Code && s.pasted != "" {
		shown = s.pasted
	}
	if s.code.Text() != shown {
		s.code.Ed.SetText(shown)
	}
	if s.copy.Clicked(gtx) {
		msg := a.T("Código copiado!")
		if shown != v.Code {
			msg = fmt.Sprintf(a.T("Link do paste copiado: %s"), shown)
		}
		a.copyText(gtx, shown, msg)
	}
	if s.goBtn.Clicked(gtx) && !s.busy {
		switch s.asVal {
		case "paste":
			s.busy = true
			code, text := v.Code, a.shareText()
			a.Say(a.T("Criando o paste…"), false)
			go func() {
				link, err := a.V.CreatePaste(context.Background(), code, text)
				s.busy = false
				if err != nil {
					a.Say(a.T("Não deu para criar o paste: ")+err.Error(), true)
				} else {
					a.mu.Lock()
					s.pasted, s.pastedFor = link, code
					a.pendingCopy = link
					a.mu.Unlock()
				}
				if a.Invalidate != nil {
					a.Invalidate()
				}
			}()
		case "link":
			if a.ShareURL != nil {
				a.copyText(gtx, a.ShareURL(v.Code), a.T("Link copiado!"))
			}
		case "text":
			a.copyText(gtx, a.shareText(), a.T("Texto do build copiado! Cole no Discord ou no fórum."))
		default:
			a.copyText(gtx, v.Code, a.T("Código copiado!"))
		}
	}
	a.mu.Lock()
	link := a.pendingCopy
	a.pendingCopy = ""
	a.mu.Unlock()
	if link != "" {
		a.copyText(gtx, link, fmt.Sprintf(a.T("Link do paste copiado: %s"), link))
	}
	if s.imp.Clicked(gtx) {
		a.importText(s.importBox.Text(), s.toVal == "this")
		s.importBox.Ed.SetText("")
	}
	var asOpts []Option
	if a.ShareURL != nil {
		asOpts = append(asOpts, Option{"link", a.T("Link (abre no navegador)")})
	}
	if !a.NoPaste {
		asOpts = append(asOpts, Option{"paste", a.T("Paste (paste.rs)")})
	}
	asOpts = append(asOpts, Option{"code", a.T("Só o código")}, Option{"text", a.T("Texto (Discord / fórum)")})
	shareNote := a.T("Paste cria um link curto no paste.rs com o resumo e o código do build (público para quem tiver o link).")
	if a.NoPaste {
		shareNote = a.T("O link abre este build no navegador de quem receber.")
	}
	toOpts := []Option{{"this", a.T("Importar para este build")}, {"new", a.T("Importar como build novo")}}
	section := func(title string, w layout.Widget) layout.Widget {
		return func(gtx C) D {
			return Box{Line: ColSep, Pad: Inset(4, 6, 6, 6)}.Layout(gtx, func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return Col(4, B(th, a.T(title)).C(ColTitleText).W(), w)(gtx)
			})
		}
	}
	return Col(6,
		section("Compartilhar", Col(4,
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(T(th, a.T("Código")+":").W()),
					layout.Rigid(Gap(4, 0)),
					layout.Flexed(1, func(gtx C) D { d, _, _ := s.code.Layout(gtx, th, 0); return d }),
					layout.Rigid(Gap(4, 0)),
					layout.Rigid(func(gtx C) D { return Button{Label: a.T("Copiar")}.Layout(gtx, th, &s.copy) }),
				)
			},
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx C) D {
						d, val, ok := s.as.Layout(gtx, th, a.Desk, asOpts, s.asVal, 0)
						if ok {
							s.asVal = val
						}
						return d
					}),
					layout.Rigid(Gap(4, 0)),
					layout.Rigid(func(gtx C) D {
						return Button{Label: a.T("Compartilhar"), Disabled: s.busy}.Layout(gtx, th, &s.goBtn)
					}),
				)
			},
			T(th, a.T("O código guarda o build inteiro (classe, atributos, equipamento, habilidades e buffs). Quem abrir o link ou colar o código vê o mesmo build.")+" "+
				shareNote).Sz(SmallText).C(ColMuted).Wrapping().W(),
		)),
		section("Importar", Col(4,
			T(th, a.T("Para importar um build, cole o link, o link do paste ou o código aqui:")).Wrapping().W(),
			func(gtx C) D {
				d, _, sub := s.importBox.Layout(gtx, th, 0)
				if sub {
					a.importText(s.importBox.Text(), s.toVal == "this")
					s.importBox.Ed.SetText("")
				}
				return d
			},
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx C) D {
						d, val, ok := s.to.Layout(gtx, th, a.Desk, toOpts, s.toVal, 0)
						if ok {
							s.toVal = val
						}
						return d
					}),
					layout.Rigid(Gap(4, 0)),
					layout.Rigid(func(gtx C) D { return Button{Label: a.T("Importar")}.Layout(gtx, th, &s.imp) }),
				)
			},
		)),
	)(gtx)
}

// ---- Atalhos e comandos (win-help) ----

func (a *App) helpBody(gtx C) D {
	th := a.Th
	sec := func(s string) layout.Widget {
		return Pad(Inset(4, 0, 1, 0), B(th, a.T(s)).C(ColTitleText).W())
	}
	row := func(k, s string) layout.Widget {
		return func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(Fixed(110, func(gtx C) D {
					return Box{Bg: rgb(0xf4f6fa), Line: rgb(0xb8c2d6), Radius: 3, Pad: Inset(0, 4, 0, 4)}.Layout(gtx, T(th, a.T(k)).Sz(SmallText).W())
				})),
				layout.Flexed(1, T(th, a.T(s)).Wrapping().W()),
			)
		}
	}
	return Col(3,
		sec("Janelas"),
		row("Alt+V", "Informações Básicas"), row("Alt+A", "Atributos"), row("Alt+Q", "Equipamentos"),
		row("Alt+S", "Habilidades"), row("Alt+B", "Bônus do Equipamento"), row("Alt+D", "Alvo / Dano"),
		row("Alt+U", "Buffs"), row("Alt+O", "Otimizador"), row("Alt+C", "Builds salvos"),
		row("Alt+L", "Compartilhar Build"), row("Alt+E", "Banco de Itens"), row("Alt+H", "Esta lista de comandos"), row("Alt+K", "Comparar itens"), row("Alt+T", "Comparar talentos"), row("Alt+G", "Comparar conjuntos"), row("Alt+W", "Janelas flutuantes (liga/desliga)"),
		T(th, a.T("A mesma tecla de novo fecha a janela (ou a traz para a frente).")).Sz(SmallText).C(ColMuted).Wrapping().W(),
		sec("Build"),
		row("Ctrl+Z", "Desfaz a última mudança do build"), row("Ctrl+Y", "Refaz a mudança desfeita"),
		row("Ctrl+S", "Salvar o build"), row("Alt+J", "Trocar a classe (lista de classes)"),
		row("Shift+clique", "Nos botões + e − dos atributos: ±10 pontos"),
		sec("Habilidades"),
		row("Clique", "Sobe um nível"), row("Shift+clique", "Sobe até o máximo que os pontos permitem"),
		row("Botão direito", "Desce um nível (Shift: zera)"),
		sec("Listas e menus"),
		row("Esc", "Fecha a lista de classes, o menu de configurações e os diálogos"),
		row("Mouse em cima", "Mostra o que o item faz (e o ganho de dano, na busca)"),
	)(gtx)
}

// ---- Chat log ----

// chatBox is the last system messages (yellow) and errors (red) on a dark
// translucent box, like the client's chat.
func (a *App) chatBox(gtx C) D {
	th := a.Th
	a.mu.Lock()
	lines := a.chat
	if len(lines) > 7 {
		lines = lines[len(lines)-7:]
	}
	lines = append([]chatLine(nil), lines...)
	a.mu.Unlock()
	if len(lines) == 0 {
		return D{}
	}
	m := op.Record(gtx.Ops)
	d := Inset(4, 6, 4, 6).Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		var ws []layout.Widget
		for _, l := range lines {
			c := ColSystem
			if l.Error {
				c = rgb(0xff6b6b)
			}
			ws = append(ws, T(th, l.S).C(c).Wrapping().W())
		}
		return Col(1, ws...)(gtx)
	})
	call := m.Stop()
	FillRound(gtx, image.Rectangle{Max: d.Size}, ColChatBG, 3, 3, 3, 3)
	call.Add(gtx.Ops)
	return d
}

// keyFocus gives the keyboard to a text box.
func keyFocus(ed *widget.Editor) key.FocusCmd { return key.FocusCmd{Tag: ed} }
