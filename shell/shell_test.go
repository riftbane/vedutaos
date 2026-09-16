package shell

import (
	"testing"

	"github.com/riftbane/veduta/v2/sim"
	"github.com/riftbane/vedutaos/card"
)

// press builds the input of one tick in which the given buttons went down.
func press(buttons ...sim.Button) sim.Input {
	var s sim.InputState
	for _, b := range buttons {
		s.Press(b)
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
	for i, c := range []struct {
		button sim.Button
		want   int
	}{
		{sim.ButtonDown, 1}, {sim.ButtonDown, 2}, {sim.ButtonDown, 0}, // past the end, back to the top
		{sim.ButtonUp, 2},                                            // and back the other way
		{sim.ButtonLeft, 2}, {sim.ButtonB, 2}, {sim.ButtonCancel, 2}, // nothing to do on the list
	} {
		var a Action
		s, a = Step(s, press(c.button))
		if a != Stay {
			t.Fatalf("%d %s: action %v", i, c.button, a)
		}
		if s.Sel != c.want {
			t.Fatalf("%d %s: selection %d, want %d", i, c.button, s.Sel, c.want)
		}
	}
}

func TestStepLaunches(t *testing.T) {
	s := State{Cards: list("one", "two"), Sel: 1}
	if got, a := Step(s, press(sim.ButtonA)); a != Launch || got.Sel != 1 {
		t.Fatalf("A: action %v selection %d", a, got.Sel)
	}
	if _, a := Step(s, press()); a != Stay {
		t.Fatalf("no buttons: action %v", a)
	}
}

// TestStepMenu: Select opens the menu over the list, where the D-pad and A work the menu
// rather than the list, and B, Cancel or Select close it. Its one entry switches off.
func TestStepMenu(t *testing.T) {
	s := State{Cards: list("one", "two"), Notice: "it did not start"}
	s, a := Step(s, press(sim.ButtonSelect))
	if a != Stay || !s.Menu || s.Notice != "" {
		t.Fatalf("Select: action %v menu %v notice %q", a, s.Menu, s.Notice)
	}
	if got, a := Step(s, press(sim.ButtonDown)); a != Stay || got.Sel != 0 || !got.Menu {
		t.Fatalf("down in the menu: action %v selection %d menu %v", a, got.Sel, got.Menu)
	}
	for _, b := range []sim.Button{sim.ButtonB, sim.ButtonCancel, sim.ButtonSelect} {
		if got, a := Step(s, press(b)); a != Stay || got.Menu {
			t.Fatalf("%s: action %v menu %v", b, a, got.Menu)
		}
	}
	if got, a := Step(s, press(sim.ButtonA)); a != Quit || got.Menu {
		t.Fatalf("A on POWER OFF: action %v menu %v", a, got.Menu)
	}
}

// TestStepWithoutGames: an empty card must not launch anything, but the menu still opens,
// so the console can be switched off.
func TestStepWithoutGames(t *testing.T) {
	s := State{}
	for _, b := range []sim.Button{sim.ButtonA, sim.ButtonDown, sim.ButtonUp} {
		got, a := Step(s, press(b))
		if a != Stay || got.Sel != 0 {
			t.Fatalf("%s on an empty card: action %v selection %d", b, a, got.Sel)
		}
	}
	s, _ = Step(s, press(sim.ButtonSelect))
	if _, a := Step(s, press(sim.ButtonA)); a != Quit {
		t.Fatal("an empty dashboard cannot be switched off")
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
	if got, _ := Step(s, press(sim.ButtonDown)); got.Notice != "" {
		t.Fatalf("notice %q", got.Notice)
	}
	if got, _ := Step(s, press()); got.Notice != "it did not start" {
		t.Fatalf("notice cleared without a press: %q", got.Notice)
	}
}
