package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"

	"ragnarok-of-building/internal/calc"
	"ragnarok-of-building/internal/store"
	"ragnarok-of-building/internal/view"
)

// App is the build editor: its windows are computed by view.Views and drawn
// with Gio.
type App struct {
	Th    *Theme
	Desk  *Desk
	V     *view.Views
	Icons *Icons
	Lang  string

	// OnTheme tells the host the theme changed (the system's title bar).
	OnTheme func(dark bool)
	// Invalidate asks the window for a new frame (set by the host).
	Invalidate func()
	// Store keeps the window layout, language and last build (StateKey)
	// and the saved builds (view.SavedKey); nil keeps nothing.
	Store store.Store
	// OpenFile asks the player for a file to open (replay import, builds
	// backup) and returns its name and contents.
	OpenFile func(title, filter string) (name string, data []byte, err error)
	// SaveFile hands the player a file (builds backup) and says where it
	// went.
	SaveFile func(name string, data []byte) (where string, err error)
	// ShareURL makes a link that opens a build code in this program (the
	// web page); nil where there is none.
	ShareURL func(code string) string
	// NoPaste: the paste service cannot be reached from here (in the
	// browser paste.rs refuses pages: it sends no CORS headers).
	NoPaste bool

	mu         sync.Mutex
	jobs       []func()       // run by the next frame (see later)
	dialogOpen atomic.Bool    // a file dialog is open
	b          calc.Build     // the build being edited (normalized by the last compute)
	view       view.BuildView // its windows
	ready      bool
	rev        int
	hist       history

	chat        []chatLine
	needSave    bool
	saveDue     time.Time // when to write the pending state; zero = nothing pending
	modal       *Modal
	savedID     string // the saved build being edited ("" = not saved)
	pendingCopy string // a paste link to copy on the next frame

	// Windows.
	wMenu, wBasic, wStatus, wEquip, wSkills, wBonus, wDamage, wBuffs, wOptimize *Window
	wSaved, wShare, wHelp, wSlot, wRdmg, wItems, wCompare, wTraits, wSets       *Window
	wSettings                                                                   *Window // a tab of the single window

	menu   menuState
	basic  basicState
	status statusState
	equip  equipState
	slot   slotState
	cmp    compareState
	damage damageState
	buffs  buffsState
	skills skillsState
	saved  savedState
	share  shareState
	replay replayState
	opt    optimizeState
	traits traitsState
	sets   setsState
	items  itemsState
	shell  shellState

	showReplayDamage bool // a replay with damage was just imported: open its window
}

type chatLine struct {
	S     string
	Error bool
}

// uiState is what data/ui.json keeps between runs.
type uiState struct {
	Lang     string                 `json:"lang"`
	Light    bool                   `json:"light,omitempty"` // the theme is dark unless set
	Floating bool                   `json:"floating"`
	Build    string                 `json:"build"`           // share code of the last build
	Saved    string                 `json:"saved,omitempty"` // its saved build's id
	Windows  map[string]windowState `json:"windows"`
}

type windowState struct {
	Open   bool `json:"open"`
	X      int  `json:"x"`
	Y      int  `json:"y"`
	Placed bool `json:"placed"`
	Min    bool `json:"min,omitempty"`
}

// NewApp builds the app over the views; it opens the last build (or the
// default one).
func NewApp(v *view.Views, th *Theme, st store.Store) *App {
	a := &App{Th: th, Desk: NewDesk(), V: v, Lang: "pt", Store: st}
	a.makeWindows()
	b := v.DefaultBuild()
	if st, ok := a.loadState(); ok {
		if st.Lang != "" {
			a.Lang = st.Lang
		}
		if d, ok := view.DecodeBuild(st.Build); ok {
			b = d
			if b.Target == 0 { // opens against the neutral dummy
				b.Target = v.DefaultTarget()
			}
		}
		a.savedID = st.Saved
		for id, ws := range st.Windows {
			if w := a.Desk.Find(id); w != nil {
				w.Open, w.Pos, w.Placed, w.Min = ws.Open, image.Pt(ws.X, ws.Y), ws.Placed, ws.Min
			}
		}
	}
	a.SetBuild(b)
	return a
}

func (a *App) makeWindows() {
	d := a.Desk
	a.wMenu = d.Add(&Window{ID: "win-menu", Title: "Menu", Col: 0, Open: true, NoShowAll: true})
	a.wBasic = d.Add(&Window{ID: "win-basic", Title: "Informações Básicas", Hotkey: "V", Col: 0, Open: true})
	a.wStatus = d.Add(&Window{ID: "win-status", Title: "Atributos", Hotkey: "A", Col: 0, Open: true})
	a.wEquip = d.Add(&Window{ID: "win-equip", Title: "Equipamentos", Hotkey: "Q", Col: 1, Open: true})
	a.wSlot = d.Add(&Window{ID: "win-slot", Title: "Equipamento", Col: 1, NoShowAll: true})
	a.wBonus = d.Add(&Window{ID: "win-bonus", Title: "Bônus do Equipamento", Hotkey: "B", Col: 1, Open: true})
	a.wDamage = d.Add(&Window{ID: "win-damage", Title: "Alvo / Dano", Hotkey: "D", Col: 2, Open: true})
	a.wBuffs = d.Add(&Window{ID: "win-buffs", Title: "Buffs e Consumíveis", Hotkey: "U", Col: 1})
	a.wBuffs.Body = a.buffsBody
	a.wSkills = d.Add(&Window{ID: "win-skills", Title: "Habilidades", Hotkey: "S", Col: 2})
	a.wSkills.Body = a.skillsBody
	a.wSkills.Footer = a.skillsFooter
	a.wSaved = d.Add(&Window{ID: "win-saved", Title: "Builds salvos", Hotkey: "C", Col: 0})
	a.wShare = d.Add(&Window{ID: "win-share", Title: "Compartilhar Build", Hotkey: "L", Col: 0})
	a.wHelp = d.Add(&Window{ID: "win-help", Title: "Atalhos e comandos", Hotkey: "H", Col: 0, NoShowAll: true})
	a.wSaved.Body = a.savedBody
	a.wShare.Body = a.shareBody
	a.wHelp.Body = a.helpBody
	d.Below = a.chatBox
	a.wRdmg = d.Add(&Window{ID: "win-rdmg", Title: "Resumo de dano do replay", Col: 2, NoShowAll: true})
	a.wRdmg.Body = a.replayBody
	a.wOptimize = d.Add(&Window{ID: "win-optimize", Title: "Otimizador", Hotkey: "O", Col: 2})
	a.wOptimize.Body = a.optimizeBody
	a.wItems = d.Add(&Window{ID: "win-items", Title: "Banco de Itens", Hotkey: "E", Col: 2})
	a.wItems.Body = a.itemsBody
	a.wItems.Footer = a.itemsFooter
	// Comparar floats over the columns and opens only when asked (the ↔
	// buttons, Alt+K).
	a.wCompare = d.Add(&Window{ID: "win-compare", Title: "Comparar itens", Hotkey: "K", Col: 2, Float: true, Width: 760, NoShowAll: true})
	a.wCompare.Body = a.compareBody
	a.wTraits = d.Add(&Window{ID: "win-traits", Title: "Comparar talentos", Hotkey: "T", Col: 2, Float: true, Width: 860, NoShowAll: true})
	a.wTraits.Body = a.traitsBody
	a.wSets = d.Add(&Window{ID: "win-sets", Title: "Comparar conjuntos", Hotkey: "G", Col: 2, Float: true, Width: 960, NoShowAll: true})
	a.wSets.Body = a.setsBody
	a.wBonus.Body = a.bonusBody
	a.wDamage.Body = a.damageBody
	a.wEquip.Body = a.equipBody
	a.wSlot.Body = a.slotBody
	a.wMenu.Body = a.menuBody
	a.wMenu.TitleExtra = a.gearButton
	a.wBasic.Body = a.basicBody
	a.wStatus.Body = a.statusBody

	// The RO-client layout, four columns (1920 px wide): the character,
	// the gear and its bonuses, the Ferramentas window with side tabs
	// (members keep their hotkeys and open their tab), then the item windows.
	// The single window (shell.go) takes the place of the columns; the
	// Float windows (the slot's dropdown, Comparar, the help) stay above it.
	a.wSettings = d.Add(&Window{ID: "win-settings", Title: "Configurações", NoShowAll: true})
	a.wSettings.Body = a.settingsMenu
	d.Main = a.shellLayout
	for _, t := range a.shellTabs() { // the first run opens on Equipamento
		t.w.Open = t.w == a.wEquip
	}
	a.wHelp.Float = true
	// Personagem and Equipamento stay windows of their own (the bonuses
	// next to the gear, not behind a tab).
	a.wBuffs.Col = 1
	// Small windows keep their size; Habilidades goes to the last column,
	// where there is room for the skill tree.
	a.wStatus.FullHeight, a.wMenu.FullHeight, a.wBasic.FullHeight = true, true, true
	a.wSkills.Col = 3
	a.wItems.Col, a.wHelp.Col = 3, 3
	// The slot editor drops down under the slot clicked.
	a.wSlot.Float, a.wSlot.AutoClose, a.wSlot.Dropdown, a.wSlot.Width = true, true, true, 430
}

func (a *App) T(s string) string { return view.Tr(a.Lang, s) }

// Build returns a copy of the build being edited: background work (the
// recompute, tooltips' comparisons) writes to the maps of the builds it is
// given, so the window's own never goes out.
func (a *App) Build() calc.Build {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.b.Clone()
}

// SetBuild replaces the build and recomputes every window in the
// background (a newer change discards an older result).
func (a *App) SetBuild(b calc.Build) {
	a.mu.Lock()
	a.record(b)
	a.rev++
	rev := a.rev
	a.b = b.Clone()
	lang := a.Lang
	a.mu.Unlock()
	go func() {
		v := a.V.Build(b, lang)
		a.mu.Lock()
		if rev == a.rev {
			a.view, a.b, a.ready = v, v.Build.Clone(), true
			for _, n := range v.Notes {
				a.chat = append(a.chat, chatLine{S: n})
			}
		}
		a.needSave = true
		a.mu.Unlock()
		if a.Invalidate != nil {
			a.Invalidate()
		}
	}()
}

// Edit applies fn to a copy of the build and recomputes.
func (a *App) Edit(fn func(b *calc.Build)) {
	b := a.Build()
	fn(&b)
	a.SetBuild(b)
}

// later runs f on the window's goroutine at the next frame.
func (a *App) later(f func()) {
	a.mu.Lock()
	a.jobs = append(a.jobs, f)
	a.mu.Unlock()
	if a.Invalidate != nil {
		a.Invalidate()
	}
}

// ErrNoFile is what OpenFile returns when the player closes the picker
// without choosing a file.
var ErrNoFile = errors.New("nenhum arquivo escolhido")

// openFile shows the file dialog and then calls then with the chosen file,
// on the window's goroutine. The dialog must not be waited for inside a
// frame: it is modal to the window and talks to it while open, so a frame
// holding the window hangs the program (Windows ends it as not
// responding).
//
// A failed read is told in the chat; a cancelled picker (ErrNoFile) is not.
func (a *App) openFile(title, filter string, then func(data []byte)) {
	if a.OpenFile == nil || !a.dialogOpen.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer a.dialogOpen.Store(false)
		_, data, err := a.OpenFile(title, filter)
		switch {
		case err == nil:
			a.later(func() { then(data) })
		case !errors.Is(err, ErrNoFile):
			a.later(func() { a.Say(err.Error(), true) })
		}
	}()
}

// Say adds a line to the chat log.
func (a *App) Say(s string, isErr bool) {
	a.mu.Lock()
	a.chat = append(a.chat, chatLine{S: s, Error: isErr})
	if len(a.chat) > 200 {
		a.chat = a.chat[len(a.chat)-200:]
	}
	a.mu.Unlock()
}

// SetLang changes the interface language.
func (a *App) SetLang(l string) {
	a.Lang = l
	a.SetBuild(a.Build())
}

// SetTheme switches between the light and the dark theme, from the next
// frame on (never halfway through one).
func (a *App) SetTheme(on bool) {
	a.later(func() {
		if on == dark {
			return
		}
		SetDark(on)
		a.mu.Lock()
		a.needSave = true
		a.mu.Unlock()
		if a.OnTheme != nil {
			a.OnTheme(on)
		}
	})
}

// Frame draws one frame.
func (a *App) Frame(gtx C) D {
	a.mu.Lock()
	ready := a.ready
	jobs := a.jobs
	a.jobs = nil
	a.mu.Unlock()
	for _, f := range jobs {
		f()
	}
	a.Desk.Hotkeys(gtx)
	a.shortcuts(gtx)
	a.updateTitles()
	if !ready {
		backdrop(gtx, gtx.Constraints.Max)
		return layout.Center.Layout(gtx, T(a.Th, a.T("Calculando…")).C(ColOnFill).W())
	}
	if a.showReplayDamage {
		a.showReplayDamage = false
		if !a.wRdmg.Open {
			a.Desk.Toggle(a.wRdmg)
		}
		a.Desk.Raise(a.wRdmg)
	}
	d := a.Desk.Layout(gtx, a.Th)
	a.layoutModal(gtx)
	a.mu.Lock()
	save := a.needSave
	a.needSave = false
	a.mu.Unlock()
	// Dragging a window changes the layout every frame: the state file is
	// written once things settle (saveDelay), not on each step.
	if a.Desk.Changed() || save {
		a.saveDue = gtx.Now.Add(saveDelay)
	}
	if !a.saveDue.IsZero() {
		if gtx.Now.Before(a.saveDue) {
			gtx.Execute(op.InvalidateCmd{At: a.saveDue})
		} else {
			a.saveDue = time.Time{}
			st := a.snapshot()
			go a.writeState(st)
		}
	}
	return d
}

// shortcuts are the global keys (Alt+W floating windows...).
func (a *App) shortcuts(gtx C) {
	for {
		ev, ok := gtx.Event(key.Filter{Name: "S", Required: key.ModShortcut})
		if !ok {
			break
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		switch e.Name {
		case "S":
			a.saveBuild(false)
		}
	}
	// A text field with the focus keeps these for its own text.
	for {
		ev, ok := gtx.Event(key.Filter{Name: "Z", Required: key.ModShortcut, Optional: key.ModShift},
			key.Filter{Name: "Y", Required: key.ModShortcut})
		if !ok {
			break
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press || a.modal != nil {
			continue
		}
		if e.Name == "Y" || e.Modifiers.Contain(key.ModShift) {
			a.Redo()
		} else {
			a.Undo()
		}
	}
}

func (a *App) updateTitles() {
	a.wMenu.Title = a.T("Menu")
	a.wBasic.Title = a.T("Informações Básicas")
	a.wStatus.Title = a.T("Atributos")
	a.wEquip.Title = a.T("Equipamentos")
	a.wBonus.Title = a.T("Bônus do Equipamento")
	a.wDamage.Title = a.T("Alvo / Dano")
	a.wBuffs.Title = a.T("Buffs e Consumíveis")
	a.wSkills.Title = a.T("Habilidades")
	a.wSaved.Title = a.T("Builds salvos")
	if n := len(a.loadSaved()); n > 0 {
		a.wSaved.Title += fmt.Sprintf(" (%d)", n)
	}
	a.wShare.Title = a.T("Compartilhar Build")
	a.wHelp.Title = a.T("Atalhos e comandos")
	a.wSettings.Title = a.T("Configurações")
	a.wRdmg.Title = a.T("Resumo de dano do replay")
	a.wOptimize.Title = a.T("Otimizador")
	a.wItems.Title = a.T("Banco de Itens")
	a.wCompare.Title = a.T("Comparar itens")
	a.wTraits.Title = a.T("Comparar talentos")
	a.wSets.Title = a.T("Comparar conjuntos")
	SearchHint = a.T("Buscar…")
	a.wSlot.Title = a.slotTitle()
}

// view returns the last computed windows.
func (a *App) View() view.BuildView {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.view
}

func (a *App) loadState() (uiState, bool) {
	return readState(a.Store)
}

// StateKey is the interface state's key in the store.
const StateKey = "data/ui.json"

func readState(s store.Store) (uiState, bool) {
	var st uiState
	if s == nil {
		return st, false
	}
	b, err := s.Read(StateKey)
	if err != nil || json.Unmarshal(b, &st) != nil {
		return st, false
	}
	return st, true
}

var saveMu sync.Mutex

// snapshot is the state to keep (taken on the UI goroutine).
func (a *App) snapshot() uiState {
	st := uiState{Lang: a.Lang, Light: !dark, Floating: a.Desk.Floating, Build: view.EncodeBuild(a.Build()), Saved: a.savedID, Windows: map[string]windowState{}}
	for _, w := range a.Desk.Windows {
		st.Windows[w.ID] = windowState{Open: w.Open, X: w.Pos.X, Y: w.Pos.Y, Placed: w.Placed, Min: w.Min}
	}
	return st
}

// saveDelay is how long the interface must stay unchanged before its state
// is written.
const saveDelay = 400 * time.Millisecond

// SaveNow writes a pending state at once (the window is closing).
func (a *App) SaveNow() {
	if !a.saveDue.IsZero() {
		a.saveDue = time.Time{}
		a.writeState(a.snapshot())
	}
}

func (a *App) writeState(st uiState) {
	if a.Store == nil {
		return
	}
	b, _ := json.MarshalIndent(st, "", " ")
	saveMu.Lock()
	defer saveMu.Unlock()
	a.Store.Write(StateKey, b)
}
