package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	veduta "github.com/riftbane/veduta/v2"
	"github.com/riftbane/veduta/v2/gfx"
	"github.com/riftbane/veduta/v2/platform"
	"github.com/riftbane/veduta/v2/script"
	"github.com/riftbane/veduta/v2/sim"
	"github.com/riftbane/vedutaos/card"
)

// fakeWindow is a panel that is not there: it hands out the events a test wants and keeps
// the frames it was given, so the whole loop runs on a machine with no display.
type fakeWindow struct {
	events   [][]platform.Event // one batch per Poll
	presents int
	closed   int
	w, h     int
}

func (f *fakeWindow) Poll() ([]platform.Event, error) {
	if len(f.events) == 0 {
		return nil, nil
	}
	b := f.events[0]
	f.events = f.events[1:]
	return b, nil
}

func (f *fakeWindow) Present(img *gfx.Image) error {
	if img.W != f.w || img.H != f.h {
		return errors.New("frame of the wrong size")
	}
	f.presents++
	return nil
}

func (f *fakeWindow) Size() (int, int) { return f.w, f.h }
func (f *fakeWindow) Close() error     { f.closed++; return nil }

func press(buttons ...sim.Button) []platform.Event {
	out := make([]platform.Event, len(buttons))
	for i, b := range buttons {
		out[i] = platform.Event{Kind: platform.Press, Button: b}
	}
	return out
}

// gamesDir builds a card with the given game folders, each holding a runnable file.
func gamesDir(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, n := range names {
		dir := filepath.Join(root, n)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "game"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestRunLaunchesAndComesBack drives the whole dashboard: it moves the selection, starts a
// game, and leaves when the game has ended — checking on the way that the panel is given
// up while the game runs.
func TestRunLaunchesAndComesBack(t *testing.T) {
	dir := gamesDir(t, "one", "two")
	// The first batch of every window is empty: a player cannot press anything before the
	// first frame is on the glass, and the dashboard draws after it has read the input.
	windows := []*fakeWindow{
		{w: 320, h: 240, events: [][]platform.Event{press(sim.ButtonDown), press(sim.ButtonA)}},
		{w: 320, h: 240, events: [][]platform.Event{{}, press(sim.ButtonSelect), press(sim.ButtonA)}},
	}
	opened := 0
	defer swap(&openWindow, func(platform.Options) (platform.Window, error) {
		if opened >= len(windows) {
			return nil, errors.New("opened too many times")
		}
		w := windows[opened]
		opened++
		return w, nil
	})()

	var ran []string
	defer swap(&launch, func(c card.Card) error {
		if windows[0].closed == 0 {
			t.Error("the game was launched while the dashboard still held the panel")
		}
		ran = append(ran, filepath.Base(c.Dir))
		return nil
	})()

	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0] != "two" {
		t.Fatalf("ran %v, want the second game", ran)
	}
	if opened != 2 {
		t.Errorf("opened the window %d times, want one per visit", opened)
	}
	for i, w := range windows {
		if w.closed == 0 {
			t.Errorf("window %d was never closed", i)
		}
		if w.presents == 0 {
			t.Errorf("window %d was never given a frame", i)
		}
	}
}

// TestRunReportsAFailedGame: a game that will not start leaves a line on the dashboard
// rather than taking the console down with it.
func TestRunReportsAFailedGame(t *testing.T) {
	dir := gamesDir(t, "broken")
	second := &fakeWindow{w: 320, h: 240, events: [][]platform.Event{{}, press(sim.ButtonSelect), press(sim.ButtonA)}}
	windows := []*fakeWindow{
		{w: 320, h: 240, events: [][]platform.Event{{}, press(sim.ButtonA)}},
		second,
	}
	opened := 0
	defer swap(&openWindow, func(platform.Options) (platform.Window, error) {
		w := windows[opened]
		opened++
		return w, nil
	})()
	defer swap(&launch, func(card.Card) error { return errors.New("fork/exec /games/broken/game: exec format error") })()

	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	if opened != 2 {
		t.Fatalf("opened %d windows", opened)
	}
	// The dashboard came back and said something about it.
	if second.presents == 0 {
		t.Error("the dashboard did not come back")
	}
}

// TestRunWithoutGames: an empty card shows the dashboard, whose settings still open, and
// can still be left.
func TestRunWithoutGames(t *testing.T) {
	dir := t.TempDir()
	w := &fakeWindow{w: 320, h: 240, events: [][]platform.Event{press(sim.ButtonA), press(sim.ButtonB), press(sim.ButtonSelect), {{Kind: platform.Release, Button: sim.ButtonA}}, press(sim.ButtonA)}}
	defer swap(&openWindow, func(platform.Options) (platform.Window, error) { return w, nil })()
	defer swap(&launch, func(card.Card) error { t.Error("launched a game that is not there"); return nil })()
	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	if w.presents == 0 {
		t.Error("nothing was drawn")
	}
}

func TestRunWithoutAGamesDirectory(t *testing.T) {
	defer swap(&openWindow, func(platform.Options) (platform.Window, error) {
		t.Error("a window was opened for a card with no games directory")
		return nil, errors.New("no")
	})()
	if err := run(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing games directory was accepted")
	}
}

// TestCloseOnTheConsole: on the console the player's close does not leave the dashboard,
// elsewhere it does.
func TestCloseOnTheConsole(t *testing.T) {
	dir := t.TempDir()
	closeEv := []platform.Event{{Kind: platform.Close}}
	w := &fakeWindow{w: 320, h: 240, events: [][]platform.Event{closeEv, press(sim.ButtonSelect), press(sim.ButtonA)}}
	defer swap(&openWindow, func(platform.Options) (platform.Window, error) { return w, nil })()
	defer swap(&leaveOnClose, false)()
	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	if len(w.events) != 0 {
		t.Fatalf("left the dashboard with %d batches unread: the close was obeyed", len(w.events))
	}
	w = &fakeWindow{w: 320, h: 240, events: [][]platform.Event{closeEv, press(sim.ButtonSelect), press(sim.ButtonA)}}
	leaveOnClose = true
	if err := run(dir); err != nil {
		t.Fatal(err)
	}
	if len(w.events) != 2 {
		t.Fatalf("%d batches unread, want the close alone read", len(w.events))
	}
}

// TestGameCommand: a program runs itself, a script game runs through this program, and a
// script game that needs a later API level does not run.
func TestGameCommand(t *testing.T) {
	cmd, err := gameCommand(card.Card{Dir: "/g/prog", Exec: "/g/prog/game"})
	if err != nil || cmd.Path != "/g/prog/game" {
		t.Fatalf("program: %v %v", cmd, err)
	}
	cmd, err = gameCommand(card.Card{Dir: "/g/lua", Script: "/g/lua/main.lua", API: script.APILevel})
	if err != nil || len(cmd.Args) != 3 || cmd.Args[1] != playCommand || cmd.Args[2] != "/g/lua" {
		t.Fatalf("script: %v %v", cmd, err)
	}
	if _, err := gameCommand(card.Card{Dir: "/g/new", Script: "/g/new/main.lua", API: script.APILevel + 1}); err == nil || notice(err) != "UPDATE VEDUTAOS" {
		t.Fatalf("a later API level: %v", err)
	}
}

func TestGameEnv(t *testing.T) {
	base := []string{"VEDUTA_SCALE=4"}
	if got := gameEnv(base, "", card.Card{Dir: "/boot/firmware/games/cave"}); len(got) != 1 {
		t.Fatalf("without saves: %v", got)
	}
	got := gameEnv(base, "/card/saves", card.Card{Dir: "/boot/firmware/games/cave"})
	if len(got) != 2 || got[1] != veduta.SaveDirEnv+"=/card/saves/cave" {
		t.Fatalf("with saves: %v", got)
	}
}

func TestNotice(t *testing.T) {
	got := notice(errors.New("fork/exec /games/broken/game: exec format error"))
	if !strings.Contains(got, "EXEC FORMAT ERROR") {
		t.Fatalf("notice %q", got)
	}
}

// swap replaces a package variable and returns the function that puts it back.
func swap[T any](p *T, v T) func() {
	old := *p
	*p = v
	return func() { *p = old }
}

func TestParseEnv(t *testing.T) {
	got := parseEnv([]byte("# settings\nVEDUTA_SCALE=4\n\nexport VEDUTA_FB = \"fb1\"\nVEDUTA_PAD='Rii'\nbroken line\n=novalue\n"))
	want := [][2]string{{"VEDUTA_SCALE", "4"}, {"VEDUTA_FB", "fb1"}, {"VEDUTA_PAD", "Rii"}}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %v, want %v", i, got[i], want[i])
		}
	}
}

func TestSameCards(t *testing.T) {
	a := []card.Card{{Dir: "/g/one", Title: "One", Exec: "/g/one/game"}}
	b := []card.Card{{Dir: "/g/one", Title: "One", Exec: "/g/one/game"}}
	if !sameCards(a, b) || sameCards(a, nil) || sameCards(a, append(b, card.Card{})) {
		t.Error("cards compared wrongly")
	}
	b[0].Icon = "/g/one/icon.png"
	if sameCards(a, b) {
		t.Error("an icon that appeared was not noticed")
	}
}
