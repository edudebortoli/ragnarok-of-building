package ui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"ragnarok-of-building/internal/store"
	"regexp"
	"strconv"
	"testing"
)

// Every color written in place has a dark version.
func TestDarkCoversColors(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	re := regexp.MustCompile(`\brgbA?\(0x([0-9a-f]{6})`)
	for _, f := range files {
		if f == "theme.go" || f == "rokit.go" { // only named colors
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			v, _ := strconv.ParseUint(m[1], 16, 32)
			if _, ok := inPlace[uint32(v)]; !ok {
				t.Errorf("%s: rgb(0x%s) has no dark version", f, m[1])
			}
		}
	}
}

func TestDarkContrast(t *testing.T) {
	light := ColText
	SetDark(true)
	defer SetDark(false)
	texts := map[string]color.NRGBA{"text": ColText, "title": ColTitleText, "up": ColUp, "down": ColDown,
		"grade": ColGrade, "link": ColLink, "muted": ColMuted, "label": ColLabel}
	for _, c := range []uint32{0, 0x333333, 0x555555, 0x666666, 0x777777, 0x888888, 0x5a6a88, 0x1d3f7a, 0xc2410c} {
		texts[fmt.Sprintf("%06x", c)] = rgb(c)
	}
	for _, c := range []uint32{0x0000ff, 0x777777, 0x990000, 0x000088, 0x008800} {
		texts[fmt.Sprintf("desc %06x", c)] = descColor(c)
	}
	for n, c := range texts {
		for bn, bg := range map[string]color.NRGBA{"panel": ColPanel, "body": ColBody, "tip": ColTipBG, "alt": ColBodyAlt, "hover": ColHover} {
			if r := contrast(c, bg); r < 4 {
				t.Errorf("%s on %s: %.1f", n, bn, r)
			}
		}
	}
	SetDark(false)
	if ColText != light {
		t.Error("the light theme did not come back")
	}
}

// The bars' captions are white.
func TestBarContrast(t *testing.T) {
	for i, b := range [][2]color.NRGBA{BarHP, BarSP, BarEXP, BarAP} {
		for _, c := range b {
			if r := contrast(ColOnFill, c); r < 3.8 {
				t.Errorf("bar %d: %.1f", i, r)
			}
		}
	}
}

// The setting is kept between runs.
func TestDarkSaved(t *testing.T) {
	h := newHarness(t)
	defer SetDark(false)
	f := store.Dir(t.TempDir())
	h.a.Store = f
	h.a.SetTheme(true)
	h.frame()
	if !dark {
		t.Fatal("not dark")
	}
	h.a.writeState(h.a.snapshot())
	SetDark(false)
	UseSavedTheme(f)
	if !dark {
		t.Error("the saved dark theme was not used")
	}
	h.a.SetTheme(false)
	h.frame()
	h.a.writeState(h.a.snapshot())
	h.a.Store = nil
	SetDark(true)
	UseSavedTheme(f)
	if dark {
		t.Error("the saved light theme was not used")
	}
	UseSavedTheme(store.Dir(t.TempDir()))
	if !dark {
		t.Error("the first run is not dark")
	}
}
