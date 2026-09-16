// Package shell is the console's dashboard: the list of games, what the pad does to it,
// and how it is drawn.
//
// It holds no files, no clock and no device. A state and an input go in, a state and an
// action come out; drawing takes a state and a batch. That is what lets the whole of what
// a player sees be checked against golden images on a machine with no panel.
package shell

import (
	"fmt"
	"image"

	"github.com/riftbane/veduta/v2/gfx"
	"github.com/riftbane/veduta/v2/gmath"
	"github.com/riftbane/veduta/v2/sim"
	"github.com/riftbane/veduta/v2/sprite"
	"github.com/riftbane/vedutaos/card"
)

// Action is what the loop should do after a step.
type Action int

// Actions.
const (
	Stay   Action = iota // keep showing the dashboard
	Launch               // run the selected game
	Quit                 // leave the dashboard: on the console, switch it off
)

// State is everything the dashboard shows.
type State struct {
	Cards   []card.Card
	Sel     int    // index of the selected game
	Notice  string // a line shown at the bottom: why a game did not start, usually
	Menu    bool   // the console's menu is open over the list
	MenuSel int    // index of the selected entry of the menu
}

// MenuItem is an entry of the console's menu.
type MenuItem struct {
	Label  string
	Action Action
}

// Menu is what Select opens on the dashboard.
var Menu = []MenuItem{{"POWER OFF", Quit}}

// Step applies one tick of the console's buttons, the same whether they come from a pad,
// the handheld's keys or a keyboard. The D-pad moves, A starts the game, Select opens the
// menu, where A chooses and B, Cancel or Select again close it.
func Step(s State, in sim.Input) (State, Action) {
	if s.Menu {
		n := len(Menu)
		switch {
		case in.JustPressed(sim.ButtonDown):
			s.MenuSel = (s.MenuSel + 1) % n
		case in.JustPressed(sim.ButtonUp):
			s.MenuSel = (s.MenuSel + n - 1) % n
		case in.JustPressed(sim.ButtonA):
			s.Menu = false
			return s, Menu[s.MenuSel].Action
		case in.JustPressed(sim.ButtonB), in.JustPressed(sim.ButtonCancel), in.JustPressed(sim.ButtonSelect):
			s.Menu = false
		}
		return s, Stay
	}
	if in.JustPressed(sim.ButtonSelect) {
		s.Menu, s.MenuSel, s.Notice = true, 0, ""
		return s, Stay
	}
	if n := len(s.Cards); n > 0 {
		switch {
		case in.JustPressed(sim.ButtonDown):
			s.Sel, s.Notice = (s.Sel+1)%n, ""
		case in.JustPressed(sim.ButtonUp):
			s.Sel, s.Notice = (s.Sel+n-1)%n, ""
		case in.JustPressed(sim.ButtonA):
			return s, Launch
		}
	}
	if s.Sel >= len(s.Cards) {
		s.Sel = 0
	}
	return s, Stay
}

// Icon is a game's picture, already uploaded to the renderer.
type Icon struct {
	Tex  gfx.TextureID
	W, H int
}

// Resources are what Draw needs beyond the state: the font and its texture, and whatever
// icons were loaded, by game folder.
type Resources struct {
	Font    *sprite.Font
	FontTex gfx.TextureID
	Icons   map[string]Icon
}

// Colours of the dashboard, in 0xAARRGGBB.
const (
	colBackground  uint32 = 0xff101418
	colHeader      uint32 = 0xff8090a0
	colText        uint32 = 0xfff4f0e0
	colDim         uint32 = 0xff70787f
	colSelected    uint32 = 0xff2a3442
	colPlaceholder uint32 = 0xff39424e
	colNotice      uint32 = 0xffd08050
)

// Draw paints the dashboard at w by h. Everything scales from the height, so the same code
// fills a 320x240 panel and a larger window.
func Draw(b *sprite.Batch, r Resources, w, h int, s State) {
	scale := max(1, h/240)
	cell := 8 * scale
	pad := 4 * scale
	b.Rect(gmath.R(0, 0, float32(w), float32(h)), colBackground)

	title := "VEDUTA"
	b.Text(r.Font, r.FontTex, float32(pad), float32(pad), scale, title, colHeader)
	count := fmt.Sprintf("%d GAMES", len(s.Cards))
	b.Text(r.Font, r.FontTex, float32(w-pad-len(count)*cell), float32(pad), scale, count, colDim)

	top := pad + cell + pad
	rowH := cell + 2*scale
	iconSide := rowH - 2*scale
	// Leave the footer a clear gap: on a small panel a list that touches it reads as one
	// block of text.
	rows := (h - top - 2*pad - cell) / rowH
	if rows < 1 {
		rows = 1
	}
	first := 0
	if s.Sel >= rows { // keep the selection on screen without scrolling past the end
		first = s.Sel - rows + 1
	}
	for i := 0; i < rows && first+i < len(s.Cards); i++ {
		c := s.Cards[first+i]
		y := top + i*rowH
		if first+i == s.Sel {
			b.Rect(gmath.R(float32(pad/2), float32(y-scale), float32(w-pad), float32(rowH)), colSelected)
		}
		x := pad
		if ic, ok := r.Icons[c.Dir]; ok && ic.W > 0 && ic.H > 0 {
			b.Image(ic.Tex, ic.W, ic.H, imageRect(ic), gmath.R(float32(x), float32(y), float32(iconSide), float32(iconSide)), colText, gfx.FilterBilinear)
		} else {
			// A game with no picture still gets a tile, so the list stays aligned.
			b.Rect(gmath.R(float32(x), float32(y), float32(iconSide), float32(iconSide)), colPlaceholder)
		}
		x += iconSide + pad
		colour := colText
		if c.Problem != "" {
			colour = colDim // listed and playable, but it did not describe itself
		}
		b.Text(r.Font, r.FontTex, float32(x), float32(y+scale), scale, clip(c.Title, (w-x-pad)/cell), colour)
	}
	if len(s.Cards) == 0 {
		msg := "NO GAMES ON THE CARD"
		b.Text(r.Font, r.FontTex, float32((w-len(msg)*cell)/2), float32(h/2-cell/2), scale, msg, colDim)
	}
	notice := s.Notice
	switch {
	case s.Menu:
		notice = "A: OK   B: BACK"
	case notice == "" && len(s.Cards) > 0:
		notice = "A: PLAY   SELECT: MENU"
	case notice == "":
		notice = "SELECT: MENU"
	}
	b.Text(r.Font, r.FontTex, float32(pad), float32(h-pad-cell), scale, clip(notice, (w-2*pad)/cell), colNotice)
	if s.Menu {
		drawMenu(b, r, w, h, scale, s.MenuSel)
	}
}

// drawMenu paints the menu as a box in the middle of the list.
func drawMenu(b *sprite.Batch, r Resources, w, h, scale, sel int) {
	cell := 8 * scale
	pad := 4 * scale
	rowH := cell + 2*scale
	widest := len("MENU")
	for _, it := range Menu {
		widest = max(widest, len(it.Label))
	}
	bw := widest*cell + 4*pad
	bh := cell + pad + len(Menu)*rowH + 2*pad
	x, y := (w-bw)/2, (h-bh)/2
	b.Rect(gmath.R(float32(x-scale), float32(y-scale), float32(bw+2*scale), float32(bh+2*scale)), colHeader)
	b.Rect(gmath.R(float32(x), float32(y), float32(bw), float32(bh)), colBackground)
	b.Text(r.Font, r.FontTex, float32(x+2*pad), float32(y+pad), scale, "MENU", colHeader)
	for i, it := range Menu {
		ry := y + pad + cell + pad + i*rowH
		if i == sel {
			b.Rect(gmath.R(float32(x+pad), float32(ry-scale), float32(bw-2*pad), float32(rowH)), colSelected)
		}
		b.Text(r.Font, r.FontTex, float32(x+2*pad), float32(ry), scale, it.Label, colText)
	}
}

func imageRect(ic Icon) image.Rectangle { return image.Rect(0, 0, ic.W, ic.H) }

// clip shortens a line to the width the panel has, marking that it was cut.
func clip(s string, cells int) string {
	if cells < 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= cells {
		return s
	}
	if cells == 1 {
		return "."
	}
	return string(r[:cells-1]) + "."
}
