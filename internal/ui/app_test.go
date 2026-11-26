package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ragnarok-of-building/internal/calc"
	"ragnarok-of-building/internal/gamedata"
	"ragnarok-of-building/internal/view"
)

// harness drives the app without a window: frames on an input router,
// with simulated pointer events.
type harness struct {
	t      *testing.T
	a      *App
	router input.Router
	ops    op.Ops
	size   image.Point
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if os.Getenv("RO_DARK") != "" { // screenshots of the dark theme
		SetDark(true)
	}
	cat, err := gamedata.Load(filepath.Join("..", "..", "data", "gen", "catalog.json.gz"))
	if err != nil {
		t.Skip("no generated catalog:", err)
	}
	v, err := view.New(view.Options{Catalog: cat})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, size: image.Pt(1440, 900)}
	h.a = NewApp(v, NewTheme(), nil)
	h.waitReady()
	h.frame()
	return h
}

func (h *harness) waitReady() {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		h.a.mu.Lock()
		ready, pending := h.a.ready, h.a.rev
		h.a.mu.Unlock()
		if ready {
			// Let a running compute land.
			time.Sleep(50 * time.Millisecond)
			h.a.mu.Lock()
			same := pending == h.a.rev
			h.a.mu.Unlock()
			if same {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatal("the build never computed")
}

func (h *harness) frame() {
	h.ops.Reset()
	gtx := layout.Context{
		Ops:         &h.ops,
		Now:         time.Now(),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(h.size),
		Source:      h.router.Source(),
	}
	h.a.Frame(gtx)
	h.router.Frame(&h.ops)
}

// click presses and releases at (x, y) and draws the frames that follow.
func (h *harness) click(x, y float32, mods ...func(*pointer.Event)) {
	pos := f32.Pt(x, y)
	ev := func(k pointer.Kind) pointer.Event {
		e := pointer.Event{Kind: k, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos, Time: time.Since(time.Time{})}
		for _, m := range mods {
			m(&e)
		}
		return e
	}
	h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos})
	h.frame()
	h.router.Queue(ev(pointer.Press))
	h.frame()
	h.router.Queue(ev(pointer.Release))
	h.frame()
	h.waitReady()
	h.frame()
}

func TestStatPlus(t *testing.T) {
	h := newHarness(t)
	before := h.a.Build().Stats[0]
	// FOR's "+" in the attributes card (single window).
	h.click(304, 196)
	if got := h.a.Build().Stats[0]; got != before+1 {
		t.Fatalf("FOR %d -> %d, want +1", before, got)
	}
}

// waitSearch waits for the item picker's results.
func (h *harness) waitSearch() {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		h.a.mu.Lock()
		done := !h.a.slot.pending
		h.a.mu.Unlock()
		if done {
			h.frame()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatal("the picker never answered")
}

func TestEquipFlow(t *testing.T) {
	h := newHarness(t)
	a := h.a
	a.openSlot(calc.Weapon)
	h.waitSearch()
	h.frame()
	a.mu.Lock()
	results := a.slot.results
	a.mu.Unlock()
	if len(results) == 0 {
		t.Fatal("no weapons offered")
	}
	var weapon view.ItemView
	for _, it := range results {
		if it.Slots > 0 {
			weapon = it
			break
		}
	}
	if weapon.ID == 0 {
		t.Skip("no slotted weapon in the first results")
	}
	a.pick(weapon.ID)
	h.waitReady()
	h.waitSearch()
	if got := a.Build().Equip[calc.Weapon].Item; got != weapon.ID {
		t.Fatalf("weapon %d, want %d", got, weapon.ID)
	}
	// A card in its first slot.
	a.slot.card = 0
	a.search(0)
	h.waitSearch()
	a.mu.Lock()
	cards := a.slot.results
	a.mu.Unlock()
	if len(cards) == 0 {
		t.Fatal("no cards offered")
	}
	a.pick(cards[0].ID)
	h.waitReady()
	h.frame()
	if got := a.Build().Equip[calc.Weapon].Cards[0]; got != cards[0].ID {
		t.Errorf("card %d, want %d", got, cards[0].ID)
	}
	// The tooltip with its comparison draws.
	a.Desk.ShowTip(func(gtx C) D {
		return a.itemTip(gtx, weapon.ID, &view.SwapView{Current: "x", Lines: []view.StatLine{{Label: "ATQ", Old: "1", New: "2", Diff: "+1", Better: true}}})
	})
	h.frame()
}

// TestSelectPick opens dropdowns and picks an option: the popup used to
// lay a click catcher over its own content, so options ignored clicks.
func TestSelectPick(t *testing.T) {
	// A long select on a window of its own: its list has a search box with
	// the focus, and Enter picks the first match.
	h := newHarness(t)
	var sel Select
	var opts []Option
	for i := range 30 {
		opts = append(opts, Option{Value: fmt.Sprint(i), Label: fmt.Sprintf("Opção %d", i)})
	}
	opts = append(opts, Option{Value: "x", Label: "Feiticeiro"})
	val := "0"
	w := h.a.Desk.Add(&Window{ID: "test-select", Title: "Teste", Col: 3, Open: true, Float: true})
	w.Body = func(gtx C) D {
		d, v, ok := sel.Layout(gtx, h.a.Th, h.a.Desk, opts, val, 120)
		if ok {
			val = v
		}
		return d
	}
	h.frame()
	h.a.Desk.OpenPopup(&sel.popup)
	h.frame()
	h.frame()
	if len(sel.shown) < selectSearchMin {
		t.Fatalf("the list has no search (%d shown)", len(sel.shown))
	}
	n := sel.q.Ed.Len()
	h.router.Queue(key.EditEvent{Range: key.Range{Start: 0, End: n}, Text: "feiticeiro"})
	h.frame()
	h.frame()
	if len(sel.shown) != 1 {
		t.Fatalf("search \"feiticeiro\" shows %d options", len(sel.shown))
	}
	h.router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	h.frame()
	h.frame()
	if h.a.Desk.popup != nil {
		t.Error("the popup stayed open after a pick")
	}
	if val != "x" {
		t.Errorf("picked %q, want the Feiticeiro option", val)
	}
	w.Open = false
	h.frame()
	// Informações Básicas ▸ the class name opens the grouped class list.
	h.click(258, 41)
	if h.a.Desk.popup == nil {
		t.Fatal("the class list did not open")
	}
	job := h.a.Build().Job
	h.click(258, 41+60)
	if h.a.Desk.popup != nil {
		t.Error("the class list stayed open after a pick")
	}
	if h.a.Build().Job == job {
		t.Error("picking in the class list changed nothing")
	}
}

// TestStatTyped types a value into FOR's box.
func TestStatTyped(t *testing.T) {
	h := newHarness(t)
	in := &h.a.status.stat[0]
	h.click(226, 196) // FOR's box (single window)
	if !in.focused {
		t.Fatal("FOR's box did not take the focus")
	}
	n := in.Ed.Len()
	h.router.Queue(key.EditEvent{Range: key.Range{Start: 0, End: n}, Text: "77"})
	h.frame()
	h.waitReady()
	h.frame()
	if got := h.a.Build().Stats[0]; got != 77 {
		t.Fatalf("FOR = %d after typing 77", got)
	}
	// Past the cap: clamped.
	h.router.Queue(key.EditEvent{Range: key.Range{Start: 0, End: in.Ed.Len()}, Text: "999"})
	h.frame()
	h.waitReady()
	h.frame()
	if got := h.a.Build().Stats[0]; got != h.a.View().R.MaxStat {
		t.Fatalf("FOR = %d after typing 999, want the cap %d", got, h.a.View().R.MaxStat)
	}
}

// TestCompareTip draws the picker's side-by-side tooltip: the equipped
// weapon next to another one.
func TestCompareTip(t *testing.T) {
	h := newHarness(t)
	a := h.a
	a.openSlot(calc.Weapon)
	h.waitSearch()
	a.mu.Lock()
	res := a.slot.results
	a.mu.Unlock()
	if len(res) < 2 {
		t.Skip("not enough weapons offered")
	}
	a.pick(res[0].ID)
	h.waitReady()
	h.waitSearch()
	if a.slotCurrent() != res[0].ID {
		t.Fatalf("slotCurrent %d, want %d", a.slotCurrent(), res[0].ID)
	}
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Constraints{Max: h.size}}
	single := a.compareTip(gtx, 0, res[1].ID, nil)
	both := a.compareTip(gtx, res[0].ID, res[1].ID, nil)
	if both.Size.X <= single.Size.X {
		t.Errorf("side by side %v not wider than one tooltip %v", both.Size, single.Size)
	}
}

// TestBestItem ranks the weapons for the slot against a target and equips
// the first at the chosen refine.
func TestBestItem(t *testing.T) {
	h := newHarness(t)
	a := h.a
	a.SetBuild(a.V.NewBuild(4252))
	a.Edit(func(b *calc.Build) { b.Target = 1039; b.Stats = [6]int{120, 100, 80, 1, 100, 1} })
	h.waitReady()
	a.openSlot(calc.Weapon)
	h.waitSearch()
	h.frame()
	a.slot.bestRefine, a.slot.bestGrade = 10, 0
	start := time.Now()
	a.runBest()
	deadline := time.Now().Add(30 * time.Second)
	for {
		a.mu.Lock()
		done := !a.slot.bestRunning
		a.mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	res := a.slot.best
	if res == nil || len(res.Items) == 0 {
		t.Fatalf("no ranking: %+v", res)
	}
	t.Logf("%d weapons ranked in %v (partial %v); best %s %s", res.Tested, time.Since(start), res.Partial, res.Items[0].Item.Name, res.Items[0].Gain.Diff)
	h.frame()
	a.pick(res.Items[0].Item.ID)
	h.waitReady()
	if e := a.Build().Equip[calc.Weapon]; e.Item != res.Items[0].Item.ID || e.Refine != 10 {
		t.Errorf("equipped %+v, want %d at +10", e, res.Items[0].Item.ID)
	}
}

// TestCompareWindow pins Chapéu de Eddga-LT, turns its effect on and
// equips the column.
func TestCompareWindow(t *testing.T) {
	h := newHarness(t)
	a := h.a
	if a.V.Item(400245, "pt") == nil {
		t.Skip("no Chapéu de Eddga-LT in the catalog")
	}
	a.SetBuild(a.V.NewBuild(4252))
	a.Edit(func(b *calc.Build) { b.Target = 1039 })
	h.waitReady()
	a.openSlot(calc.HeadTop)
	h.waitSearch()
	a.addCompare(400245)
	a.addCompare(400245) // already pinned
	if len(a.cmp.cols) != 2 || !a.wCompare.Open {
		t.Fatalf("columns %+v, open %v", a.cmp.cols, a.wCompare.Open)
	}
	key := calc.ItemEffectKey(400245)
	a.cmp.cols[1].Fx = map[string]bool{key: true}
	wait := func() {
		deadline := time.Now().Add(10 * time.Second)
		for {
			h.frame()
			a.mu.Lock()
			done := !a.cmp.busy && a.cmp.res != nil
			a.mu.Unlock()
			if done || time.Now().After(deadline) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	wait()
	res := a.cmp.res
	if len(res.Cols) != 2 || len(res.Cols[1].Effects) == 0 || !res.Cols[1].Effects[0].On {
		t.Fatalf("columns: %+v", res.Cols)
	}
	if res.Rows[0].Better[1] != 1 {
		t.Errorf("Eddga-LT with its effect should add damage: %+v", res.Rows[0])
	}
	if a.Build().Buffs[key] != 0 {
		t.Error("the column's switch reached the build before Equipar")
	}
	a.SetBuild(a.V.CompareBuild(a.Build(), calc.HeadTop, a.cmp.cols[1]))
	h.waitReady()
	if b := a.Build(); b.Equip[calc.HeadTop].Item != 400245 || b.Buffs[key] != 1 {
		t.Errorf("Equipar: %+v %v", b.Equip[calc.HeadTop], b.Buffs)
	}
}

// TestOpenFileNonBlocking: frames go on while the file dialog is open (the
// replay import used to wait for it inside a frame and hang the window).
func TestOpenFileNonBlocking(t *testing.T) {
	h := newHarness(t)
	a := h.a
	release := make(chan struct{})
	a.OpenFile = func(title, filter string) (string, []byte, error) {
		<-release
		return "x.rrf", []byte("x"), nil
	}
	got := ""
	a.openFile("t", "f", func(b []byte) { got = string(b) })
	a.openFile("t", "f", func([]byte) { t.Error("a second dialog opened") })
	done := make(chan struct{})
	go func() { h.frame(); h.frame(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("frames blocked by the open dialog")
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for got == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		h.frame()
	}
	if got != "x" {
		t.Fatalf("callback got %q", got)
	}
}

// TestOptimizerItems runs the optimizer with items at +10 and applies its
// best suggestion.
func TestOptimizerItems(t *testing.T) {
	h := newHarness(t)
	a := h.a
	a.SetBuild(a.V.NewBuild(4252))
	a.Edit(func(b *calc.Build) { b.Target = 1039 })
	h.waitReady()
	a.opt.refine = 10
	a.runOptimizer()
	deadline := time.Now().Add(30 * time.Second)
	for {
		a.mu.Lock()
		busy := a.opt.busy
		a.mu.Unlock()
		if !busy || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	res := a.opt.res
	if res == nil || len(res.Groups) == 0 || len(res.Groups[0].Options) == 0 {
		t.Fatalf("no suggestions: %+v", res)
	}
	var o *view.OptOption
	for _, g := range res.Groups {
		if g.Kind == "item" && len(g.Options) > 0 {
			o = &g.Options[0]
			break
		}
	}
	if o == nil {
		t.Fatal("no item suggested")
	}
	a.applyOpt(*o)
	h.waitReady()
	e := a.Build().Equip[o.Slot]
	if e.Item != o.Item.ID || e.Refine != o.Refine {
		t.Errorf("applied %+v, want %d at +%d", e, o.Item.ID, o.Refine)
	}
	h.frame()
}

// TestNewBuildResets: Novo, then a class from the list, starts a blank build
// (closing the list used to clear the mode first, keeping the old gear).
func TestNewBuildResets(t *testing.T) {
	h := newHarness(t)
	a := h.a
	a.SetBuild(a.V.NewBuild(4252))
	a.Edit(func(b *calc.Build) { b.Equip = map[string]calc.Equip{calc.Armor: {Item: 2301}} })
	h.waitReady()
	a.basic.newMode = true
	a.Desk.OpenPopup(&a.basic.classDD)
	if !a.wBasic.Open {
		a.Desk.Toggle(a.wBasic)
	}
	h.frame()
	cl := a.basic.jobs[4252]
	if cl == nil {
		t.Fatal("class list not drawn")
	}
	cl.Click()
	h.frame()
	h.waitReady()
	if n := len(a.Build().Equip); n != 0 {
		t.Errorf("new build kept %d item(s): %v", n, a.Build().Equip)
	}
}

// TestShellTabs: one tab window shows at a time; a hotkey or a window
// opened from elsewhere (the replay import) becomes the tab shown, and
// Comparar floats above the single window.
func TestShellTabs(t *testing.T) {
	h := newHarness(t)
	a, d := h.a, h.a.Desk
	h.frame()
	if a.shell.cur != a.wEquip {
		t.Fatalf("first tab %s, want Equipamento", a.shell.cur.ID)
	}
	d.Toggle(a.wSaved) // Alt+C
	h.frame()
	if a.shell.cur != a.wSaved || !a.wSaved.Open || a.wEquip.Open {
		t.Fatalf("after Alt+C the tab is %s", a.shell.cur.ID)
	}
	d.Toggle(a.wSaved) // again: the tab stays
	h.frame()
	if a.shell.cur != a.wSaved || !a.wSaved.Open {
		t.Fatal("Alt+C on the tab shown closed it")
	}
	a.wRdmg.Open = true // the replay import opens its window directly
	h.frame()
	if a.shell.cur != a.wRdmg {
		t.Fatalf("opening a tab window did not show it: %s", a.shell.cur.ID)
	}
	if a.wCompare.Open {
		t.Fatal("Comparar should start closed")
	}
	a.showCompare()
	h.frame()
	if !a.wCompare.Open || a.wCompare.rect.Empty() {
		t.Errorf("Comparar did not float when asked: open %v at %v", a.wCompare.Open, a.wCompare.rect)
	}
}
