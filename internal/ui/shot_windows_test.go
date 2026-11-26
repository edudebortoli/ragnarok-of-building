//go:build windows

package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/gpu/headless"

	"ragnarok-of-building/internal/calc"
	"ragnarok-of-building/internal/store"
)

// shot draws the current frame into a PNG (Direct3D, no window).
func (h *harness) shot(name string) {
	dir := os.Getenv("RO_SHOTS")
	if dir == "" {
		return
	}
	h.frame()
	w, err := headless.NewWindow(h.size.X, h.size.Y)
	if err != nil {
		h.t.Fatal(err)
	}
	defer w.Release()
	if err := w.Frame(&h.ops); err != nil {
		h.t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: h.size})
	if err := w.Screenshot(img); err != nil {
		h.t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img)
}

// TestShots renders the windows to RO_SHOTS (skipped without it), to look
// at them next to the old web version.
func TestShots(t *testing.T) {
	if os.Getenv("RO_SHOTS") == "" {
		t.Skip("set RO_SHOTS to a folder")
	}
	h := newHarness(t)
	a := h.a
	a.SetBuild(a.V.NewBuild(4252))
	h.waitReady()
	h.shot("01-start")
	a.Edit(func(b *calc.Build) { b.Pet, b.PetIntimacy = 9121, 850 }) // Orc Herói, Cordial
	h.waitReady()
	h.shot("01-pet")
	a.Edit(func(b *calc.Build) { b.Pet, b.PetIntimacy = 0, 0 })
	h.waitReady()
	a.openSlot(calc.Weapon)
	h.waitSearch()
	h.shot("02-slot-weapon")
	a.mu.Lock()
	res := a.slot.results
	a.mu.Unlock()
	for _, it := range res {
		if it.Slots >= 2 {
			a.pick(it.ID)
			break
		}
	}
	h.waitReady()
	h.waitSearch()
	h.shot("03-weapon-equipped")
	a.openSlot(calc.Weapon) // the editor on an equipped item: its description
	h.waitReady()
	h.shot("03-slot-editor")
	a.equipDone()

	// A target and learned skills: the damage window and its table.
	a.Edit(func(b *calc.Build) {
		b.Target = 1039
		b.Stats = [6]int{120, 100, 80, 1, 100, 1}
		b.Skills = map[int]int{}
		for _, s := range a.V.Catalog().Jobs[4252].Tree {
			if sk := a.V.Catalog().Skills[s]; sk != nil {
				b.Skills[s] = sk.MaxLv
			}
		}
	})
	h.waitReady()
	h.shot("04-damage")
	a.Desk.Toggle(a.wBuffs)
	a.Desk.Toggle(a.wSlot)
	h.shot("05-buffs")
	a.Desk.Toggle(a.wSkills)
	a.Desk.Toggle(a.wDamage)
	h.shot("06-skills")
	a.wSlot.Open = false
	a.shellSelect(a.wSkills) // the Habilidades tab: points and Resetar under the tree
	h.shot("07-skills-tab")
}

// TestShotsReplay imports RO_REPLAY (a .rrf) and renders the recap, the
// saved builds, the share window and a dialog.
func TestShotsReplay(t *testing.T) {
	rrf := os.Getenv("RO_REPLAY")
	if os.Getenv("RO_SHOTS") == "" || rrf == "" {
		t.Skip("set RO_SHOTS and RO_REPLAY")
	}
	h := newHarness(t)
	a := h.a
	a.Store = store.Dir(t.TempDir())
	buf, err := os.ReadFile(rrf)
	if err != nil {
		t.Fatal(err)
	}
	a.importReplayFile(buf)
	deadline := time.Now().Add(20 * time.Second)
	for a.replayDamage() == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	h.waitReady()
	h.frame()
	h.shot("07-replay")
	a.Desk.Toggle(a.wRdmg)
	a.Desk.Toggle(a.wSaved)
	a.Desk.Toggle(a.wShare)
	a.saveBuild(true)
	h.shot("08-save-dialog")
	a.modal.OnClose(0, "Meu build")
	a.modal = nil
	h.shot("09-saved-share")
	a.Desk.Toggle(a.wOptimize)
	a.Desk.Toggle(a.wItems)
	a.items.q.Ed.SetText("poring")
	a.Edit(func(b *calc.Build) { b.Target = 1039 })
	h.waitReady()
	a.runOptimizer()
	deadline = time.Now().Add(40 * time.Second)
	for a.opt.res == nil && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	h.frame()
	h.shot("10-optimize-items")
	if len(a.loadSaved()) != 1 {
		t.Error("the build was not saved")
	}
}
