package shell

import (
	"testing"

	"github.com/riftbane/veduta/sim"
	"github.com/riftbane/vedutaos/card"
)

// press builds the input of one tick in which the given keys went down.
func press(codes ...string) sim.Input {
	var s sim.InputState
	for _, c := range codes {
		s.KeyDown(c)
	}
	return s.Next()
}

func list(titles ...string) []card.Card {
	out := make([]card.Card, len(titles))
	for i, t := range titles {
		out[i] = card.Card{Dir: "/games/" + t, Title: t, Exec: "/games/" + t + "/game"}
	}
	return out
}

func TestStepMovesAndWraps(t *testing.T) {
	s := State{Cards: list("one", "two", "three")}
	for _, c := range []struct {
		key  string
		want int
	}{
		{"ArrowDown", 1}, {"ArrowDown", 2}, {"ArrowDown", 0}, // past the end, back to the top
		{"ArrowUp", 2},           // and back the other way
		{"KeyW", 1}, {"KeyS", 2}, // the same keys a keyboard sends
	} {
		var a Action
		s, a = Step(s, press(c.key))
		if a != Stay {
			t.Fatalf("%s: action %v", c.key, a)
		}
		if s.Sel != c.want {
			t.Fatalf("%s: selection %d, want %d", c.key, s.Sel, c.want)
		}
	}
}

func TestStepLaunchesAndQuits(t *testing.T) {
	s := State{Cards: list("one", "two"), Sel: 1}
	for _, key := range []string{"Space", "Enter"} {
		got, a := Step(s, press(key))
		if a != Launch || got.Sel != 1 {
			t.Fatalf("%s: action %v selection %d", key, a, got.Sel)
		}
	}
	if _, a := Step(s, press("Escape")); a != Quit {
		t.Fatalf("Escape: action %v", a)
	}
	if _, a := Step(s, press()); a != Stay {
		t.Fatalf("no keys: action %v", a)
	}
}

// TestStepWithoutGames: an empty card must not launch anything, but must still be able to
// leave the dashboard.
func TestStepWithoutGames(t *testing.T) {
	s := State{}
	for _, key := range []string{"Space", "Enter", "ArrowDown", "ArrowUp"} {
		got, a := Step(s, press(key))
		if a != Stay || got.Sel != 0 {
			t.Fatalf("%s on an empty card: action %v selection %d", key, a, got.Sel)
		}
	}
	if _, a := Step(s, press("Escape")); a != Quit {
		t.Fatal("an empty dashboard cannot be left")
	}
}

// TestStepClampsAfterTheListShrinks covers a card being pulled out: the selection can be
// past the end of a list that just got shorter.
func TestStepClampsAfterTheListShrinks(t *testing.T) {
	s := State{Cards: list("one"), Sel: 5}
	s, a := Step(s, press())
	if a != Stay || s.Sel != 0 {
		t.Fatalf("action %v selection %d, want the first game", a, s.Sel)
	}
}

// TestStepClearsTheNotice: a message about the last failure goes away as soon as the
// player moves, rather than sitting there for ever.
func TestStepClearsTheNotice(t *testing.T) {
	s := State{Cards: list("one", "two"), Notice: "it did not start"}
	if got, _ := Step(s, press("ArrowDown")); got.Notice != "" {
		t.Fatalf("notice %q", got.Notice)
	}
	if got, _ := Step(s, press()); got.Notice != "it did not start" {
		t.Fatalf("notice cleared without a press: %q", got.Notice)
	}
}
