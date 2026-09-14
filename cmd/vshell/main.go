// Command vshell is the console's dashboard: it lists the games on the card, runs the one
// the player chooses, and comes back when that game ends.
//
// There is one panel and one pad, so the dashboard gives them up while a game is running:
// it closes its window before launching and opens it again afterwards. On a desktop the
// same program opens an ordinary window, which is how it is developed.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/riftbane/veduta/gfx"
	"github.com/riftbane/veduta/gfx/soft"
	"github.com/riftbane/veduta/platform"
	"github.com/riftbane/veduta/sim"
	"github.com/riftbane/veduta/sprite"
	"github.com/riftbane/vedutaos/card"
	"github.com/riftbane/vedutaos/shell"
)

// The panel refreshes twenty times a second, and so does the dashboard.
const tickRate = 20

// defaultGames is where a card's games are: the partition a PC sees when the card is
// plugged into it.
const defaultGames = "/boot/firmware/games"

// Seams: tests drive the whole loop through a window that is not a window and a launcher
// that runs nothing.
var (
	openWindow = platform.Open
	launch     = runGame
)

func main() {
	dir := flag.String("games", envOr("VEDUTAOS_GAMES", defaultGames), "directory holding the game folders")
	flag.Parse()
	if err := run(*dir); err != nil {
		fmt.Fprintln(os.Stderr, "vshell:", err)
		os.Exit(1)
	}
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// run shows the dashboard until the player leaves it, launching games in between.
func run(dir string) error {
	var state shell.State
	for {
		// The card is read again every time: a game may have been added or removed while
		// another was playing, and a folder can go missing under us.
		cards, err := card.Scan(dir)
		if err != nil {
			return err
		}
		state.Cards = cards
		next, action, err := show(state)
		if err != nil {
			return err
		}
		state = next
		if action != shell.Launch {
			return nil // Quit, or the window was closed
		}
		if state.Sel >= len(state.Cards) {
			continue
		}
		if err := launch(state.Cards[state.Sel]); err != nil {
			state.Notice = notice(err)
		} else {
			state.Notice = ""
		}
	}
}

// show runs the dashboard until the player launches a game or leaves, with the window open
// only for as long as that takes.
func show(state shell.State) (shell.State, shell.Action, error) {
	win, err := openWindow(platform.Options{Title: "VedutaOS", Width: 320, Height: 240})
	if err != nil {
		return state, shell.Quit, err
	}
	defer win.Close()

	r := soft.New(soft.Options{})
	defer r.Close()
	font := sprite.DefaultFont()
	fontTex, err := r.CreateTexture(font.TextureData())
	if err != nil {
		return state, shell.Quit, err
	}
	res := shell.Resources{Font: font, FontTex: fontTex, Icons: loadIcons(r, state.Cards)}

	var (
		in     sim.InputState
		dl     gfx.DrawList
		fb     *gfx.Framebuffer
		period = time.Second / tickRate
		next   = time.Now()
	)
	for {
		events, err := win.Poll()
		if err != nil {
			return state, shell.Quit, err
		}
		for _, e := range events {
			switch e.Kind {
			case platform.KeyDown:
				in.KeyDown(e.Code)
			case platform.KeyUp:
				in.KeyUp(e.Code)
			case platform.Close:
				return state, shell.Quit, nil
			case platform.FocusLost:
				in.ReleaseAll()
			}
		}
		var action shell.Action
		state, action = shell.Step(state, in.Next())
		if action != shell.Stay {
			return state, action, nil
		}
		w, h := win.Size()
		if w > 0 && h > 0 {
			if fb == nil || fb.W != w || fb.H != h {
				fb = gfx.NewFramebuffer(w, h, false)
			}
			dl.Reset()
			b := sprite.Begin(&dl, w, h)
			shell.Draw(b, res, w, h, state)
			b.End()
			if err := r.Begin(fb); err != nil {
				return state, shell.Quit, err
			}
			if err := r.Draw(&dl); err != nil {
				return state, shell.Quit, err
			}
			if err := r.End(); err != nil {
				return state, shell.Quit, err
			}
			if err := win.Present(fb.Image()); err != nil {
				return state, shell.Quit, err
			}
		}
		next = next.Add(period)
		if d := time.Until(next); d > 0 {
			time.Sleep(d)
		} else if -d > 5*period {
			next = time.Now() // far behind: skip ahead rather than race
		}
	}
}

// loadIcons uploads the picture of every game that has one. A game without an icon, or
// with one that cannot be read, simply gets the placeholder tile.
func loadIcons(r *soft.Renderer, cards []card.Card) map[string]shell.Icon {
	icons := map[string]shell.Icon{}
	for _, c := range cards {
		if c.Icon == "" {
			continue
		}
		f, err := os.Open(c.Icon)
		if err != nil {
			continue
		}
		img, err := gfx.DecodePNG(f)
		f.Close()
		if err != nil || img.W <= 0 || img.H <= 0 {
			continue
		}
		tex, err := r.CreateTexture(&gfx.TextureData{Levels: gfx.BuildMips(img), Wrap: gfx.WrapClamp})
		if err != nil {
			continue
		}
		icons[c.Dir] = shell.Icon{Tex: tex, W: img.W, H: img.H}
	}
	return icons
}

// runGame runs a game and waits for it. The card was written by a PC, where a file has no
// permission to execute, so the bit is set here rather than asked of the player.
func runGame(c card.Card) error {
	if fi, err := os.Stat(c.Exec); err == nil && fi.Mode().Perm()&0o111 == 0 {
		os.Chmod(c.Exec, fi.Mode().Perm()|0o755)
	}
	cmd := exec.Command(c.Exec)
	cmd.Dir = c.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// notice turns a failure into the single line the dashboard has room for.
func notice(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s)-i < 40 {
		s = s[i+2:]
	}
	return strings.ToUpper(filepath.Base(s))
}
