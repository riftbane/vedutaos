package shell

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/riftbane/veduta/v2/gfx"
	"github.com/riftbane/veduta/v2/gfx/soft"
	"github.com/riftbane/veduta/v2/sprite"
	"github.com/riftbane/vedutaos/card"
)

var update = flag.Bool("update-golden", false, "rewrite the reference images")

// render draws a dashboard exactly as the console will, and returns the frame. The
// renderer is the engine's own, so what these images show is what the panel will show.
func render(t *testing.T, w, h int, s State, icons map[string]Icon) *gfx.Image {
	t.Helper()
	r := soft.New(soft.Options{})
	t.Cleanup(func() { r.Close() })
	font := sprite.DefaultFont()
	tex, err := r.CreateTexture(font.TextureData())
	if err != nil {
		t.Fatal(err)
	}
	var dl gfx.DrawList
	b := sprite.Begin(&dl, w, h)
	Draw(b, Resources{Font: font, FontTex: tex, Icons: icons}, w, h, s)
	b.End()
	fb := gfx.NewFramebuffer(w, h, false)
	if err := r.Begin(fb); err != nil {
		t.Fatal(err)
	}
	if err := r.Draw(&dl); err != nil {
		t.Fatal(err)
	}
	if err := r.End(); err != nil {
		t.Fatal(err)
	}
	return fb.Image()
}

// golden compares a frame with the reference image of the same name, or rewrites it with
// -update-golden. Images are compared by their pixels, so a different PNG encoder cannot
// fail them.
func golden(t *testing.T, name string, img *gfx.Image) {
	t.Helper()
	path := filepath.Join("testdata", name+".png")
	var buf bytes.Buffer
	if err := img.EncodePNG(&buf); err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s — look at it before committing it", path)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update-golden once the picture is right)", err)
	}
	want, err := gfx.DecodePNG(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if want.W != img.W || want.H != img.H {
		t.Fatalf("%s is %dx%d, the frame is %dx%d", name, want.W, want.H, img.W, img.H)
	}
	diff := 0
	for i, p := range img.Pix {
		if p != want.Pix[i] {
			diff++
		}
	}
	if diff != 0 {
		bad := filepath.Join("testdata", name+".got.png")
		os.WriteFile(bad, buf.Bytes(), 0o644)
		t.Errorf("%s differs in %d pixels; the frame drawn is in %s", name, diff, bad)
	}
}

func games(titles ...string) []card.Card {
	out := make([]card.Card, len(titles))
	for i, t := range titles {
		out[i] = card.Card{Dir: "/games/" + t, Title: t, Exec: "/games/" + t + "/game"}
	}
	return out
}

// TestDrawGolden pins what the panel shows in the cases that matter: an empty card, one
// game, a full list, a list longer than the panel, a title too long for it, a game whose
// description was damaged, a notice, and the menu.
func TestDrawGolden(t *testing.T) {
	// More games than the panel can show at once, so the scrolling case really scrolls.
	titles := make([]string, 30)
	for i := range titles {
		titles[i] = fmt.Sprintf("Game %02d", i+1)
	}
	long := games(titles...)
	broken := games("described", "damaged")
	broken[1].Problem = "card.json: unexpected end of JSON input"
	for _, c := range []struct {
		name string
		s    State
	}{
		{"empty", State{}},
		{"one", State{Cards: games("Cave of Gems")}},
		{"few", State{Cards: games("Cave of Gems", "Sky Race", "Tunnel"), Sel: 1}},
		{"many", State{Cards: long, Sel: 0}},
		{"scrolled", State{Cards: long, Sel: len(long) - 1}},
		{"long-title", State{Cards: games("A Title Far Too Long For This Little Panel")}},
		{"damaged", State{Cards: broken, Sel: 1}},
		{"notice", State{Cards: games("Cave of Gems"), Notice: "GAME STOPPED: EXIT CODE 2"}},
		{"menu", State{Cards: games("Cave of Gems", "Sky Race", "Tunnel"), Sel: 1, Menu: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			golden(t, c.name, render(t, 320, 240, c.s, nil))
		})
	}
}

// TestDrawScales checks the dashboard fills a larger window too, for development on a
// desktop.
func TestDrawScales(t *testing.T) {
	img := render(t, 640, 480, State{Cards: games("Cave of Gems", "Sky Race"), Sel: 0}, nil)
	golden(t, "desktop", img)
}
