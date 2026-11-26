package ui

import (
	"image/color"
	"math"
	"ragnarok-of-building/internal/store"
)

// The dark theme. The named colors (theme.go, rokit.go) are variables that
// SetDark switches; the colors written in place go through rgb, which looks
// up their dark version. Only the window's goroutine draws and switches.

var dark bool

// IsDark tells whether the dark theme is on.
func IsDark() bool { return dark }

// named are the variable colors and their dark versions.
var named = []struct {
	v    *color.NRGBA
	dark uint32
}{
	{&ColText, 0xd9dde5},
	{&ColTitleA, 0x3a4660}, {&ColTitleB, 0x28324a}, {&ColTitleText, 0xc9dafc},
	{&ColBorder, 0x4b566c}, {&ColBody, 0x20242c}, {&ColBodyAlt, 0x2a2f39},
	{&ColBtnA, 0x3d4452}, {&ColBtnB, 0x2d333f},
	{&ColSelect, 0x2f4f86}, {&ColHover, 0x2b3547},
	{&ColUp, 0x6fd07f}, {&ColDown, 0xff8a80}, {&ColGrade, 0xf08be0},
	{&ColOn, 0x44763a}, {&ColLink, 0x86b4ff},
	{&ColMuted, 0x9aa2b0}, {&ColLabel, 0xaab2d0},
	{&ColListBorder, 0x3d4554}, {&ColSep, 0x3a414e},
	{&ColPanel, 0x181b21}, {&ColBtnTop, 0x4a5366}, {&ColTipBG, 0x262b35},
	{&ColSkyA, 0x131a26}, {&ColSkyB, 0x182029}, {&ColSkyC, 0x10161a},
	{&colTabOff, 0x2c323d}, {&colTabOffB, 0x232831},
	{&colScrollBg, 0x1e222a},
	{&colThumbA, 0x56719f}, {&colThumbB, 0x3d5684}, {&colThumbLine, 0x6d8dc4},
	{&colArrow, 0x8fb0e8},
	{&colFilterOn, 0x3a4f78}, {&colFilterOnB, 0x2d3f63},
	{&colFilterOff, 0x2c313b}, {&colFilterOffB, 0x232831},
}

// inPlace are the dark versions of the colors written in place.
var inPlace = map[uint32]uint32{
	// Text.
	0x000000: 0xd9dde5, 0x2b2b2b: 0xd9dde5, 0x333333: 0xd9dde5,
	0x555555: 0xa9b0bd, 0x666677: 0x9aa2b0, 0x666666: 0x9aa2b0,
	0x777777: 0x9aa2b0, 0x888888: 0x939baa, 0x999999: 0x8a92a1,
	0x5a6a88: 0x9fb0d0, 0x6a7894: 0x9fb0d0,
	0x1d3f7a: 0x9fc2ff, 0x1d4fa8: 0x86b4ff, 0xc2410c: 0xffa066,
	// Lines and fills.
	0x7f9db9: 0x4f6480, 0x7d9bd1: 0x4c648f, 0x7f9fd9: 0x5f82c4,
	0x8a94a8: 0x4b566c, 0xb8c2d6: 0x4b566c, 0xe3e6ec: 0x2f3541,
	0xa08a3a: 0x8f7b36, 0x9a9a9a: 0x5b6270,
	// Backgrounds.
	0xf0d98a: 0x5e5123, 0xd9dfeb: 0x343b49, 0xc9d6ee: 0x353f54,
	0xeef1f6: 0x2c323d, 0xc8d0de: 0x232831, 0xdce8fb: 0x2b3547,
	0xeef4ff: 0x253046, 0xf6f8fc: 0x1f242d, 0xf3f5f9: 0x1f242d,
	0xfafafa: 0x1c2027, 0xf3f7ff: 0x212a3a, 0xeef0f4: 0x232831,
	0xe9eef8: 0x333f58, 0xcfd8ea: 0x28324a, 0xfbfcff: 0x1c2027,
	0xe4e6ea: 0x2a2f39, 0xfff4cf: 0x453c1e, 0xf4f6fa: 0x2a303b,
	0xff6b6b: 0xff7b7b, 0xffffff: 0xffffff, // chat error; the gear's glow
}

// lightOf keeps the named colors' light versions.
var lightOf []color.NRGBA

// SetDark turns the dark theme on or off.
func SetDark(on bool) {
	if lightOf == nil {
		for _, n := range named {
			lightOf = append(lightOf, *n.v)
		}
	}
	dark = on
	for i, n := range named {
		*n.v = lightOf[i]
		if on {
			*n.v = plain(n.dark)
		}
	}
}

// UseSavedTheme applies the theme the store keeps: dark unless the player
// chose the light one.
func UseSavedTheme(s store.Store) {
	st, _ := readState(s)
	SetDark(!st.Light)
}

// plain is a color that is the same in both themes.
func plain(c uint32) color.NRGBA {
	return color.NRGBA{R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c), A: 0xff}
}

// rgb is a color in the current theme.
func rgb(c uint32) color.NRGBA {
	if dark {
		if d, ok := inPlace[c]; ok {
			c = d
		}
	}
	return plain(c)
}

// descColor is a color of the client's descriptions. They are written for a
// white box: on dark ones, they get lighter until they can be read.
func descColor(c uint32) color.NRGBA {
	out := plain(c)
	if !dark {
		return out
	}
	for i := 0; i < 20 && contrast(out, ColHover) < 4.5; i++ {
		f := func(v uint8) uint8 { return v + uint8((255-int(v)+9)/10) }
		out.R, out.G, out.B = f(out.R), f(out.G), f(out.B)
	}
	return out
}

// contrast is the WCAG contrast ratio of two colors.
func contrast(a, b color.NRGBA) float64 {
	lum := func(c color.NRGBA) float64 {
		f := func(v uint8) float64 {
			x := float64(v) / 255
			if x <= 0.03928 {
				return x / 12.92
			}
			return math.Pow((x+0.055)/1.055, 2.4)
		}
		return 0.2126*f(c.R) + 0.7152*f(c.G) + 0.0722*f(c.B)
	}
	x, y := lum(a), lum(b)
	return (max(x, y) + 0.05) / (min(x, y) + 0.05)
}
