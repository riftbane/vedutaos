package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/riftbane/veduta/gfx"
	"github.com/riftbane/veduta/platform"
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

func (f *fakeWindow) Size() (int, int)          { return f.w, f.h }
func (f *fakeWindow) SetPointerLock(bool) error { return nil }
func (f *fakeWindow) Close() error              { f.closed++; return nil }

func keys(codes ...string) []platform.Event {
	out := make([]platform.Event, len(codes))
	for i, c := range codes {
		out[i] = platform.Event{Kind: platform.KeyDown, Code: c}
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
		{w: 320, h: 240, events: [][]platform.Event{keys("ArrowDown"), keys("Space")}},
		{w: 320, h: 240, events: [][]platform.Event{{}, keys("Escape")}},
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
	second := &fakeWindow{w: 320, h: 240, events: [][]platform.Event{{}, keys("Escape")}}
	windows := []*fakeWindow{
		{w: 320, h: 240, events: [][]platform.Event{{}, keys("Space")}},
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

// TestRunWithoutGames: an empty card shows the dashboard and can still be left.
func TestRunWithoutGames(t *testing.T) {
	dir := t.TempDir()
	w := &fakeWindow{w: 320, h: 240, events: [][]platform.Event{keys("Space"), keys("Escape")}}
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
