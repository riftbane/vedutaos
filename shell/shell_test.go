package shell

import (
	"testing"

	"github.com/riftbane/veduta/v2/sim"
	"github.com/riftbane/vedutaos/card"
	"github.com/riftbane/vedutaos/update"
	"github.com/riftbane/vedutaos/wifi"
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
		{sim.ButtonDown, 1}, {sim.ButtonDown, 2}, {sim.ButtonDown, 3}, // the Settings row
		{sim.ButtonDown, 0}, // past the end, back to the top
		{sim.ButtonUp, 3},   // and back the other way
		{sim.ButtonUp, 2},
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
// so the console can be switched off, and Settings is still there.
func TestStepWithoutGames(t *testing.T) {
	s := State{}
	for _, b := range []sim.Button{sim.ButtonDown, sim.ButtonUp} {
		got, a := Step(s, press(b))
		if a != Stay || got.Sel != 0 {
			t.Fatalf("%s on an empty card: action %v selection %d", b, a, got.Sel)
		}
	}
	if got, a := Step(s, press(sim.ButtonA)); a != Stay || got.Screen != Settings {
		t.Fatalf("A on an empty card: action %v screen %v, want the settings", a, got.Screen)
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

// TestStepSettingsAndBack: A on the row after the games opens the settings, A on WI-FI
// opens the networks and asks for a search, and B goes back a screen at a time.
func TestStepSettingsAndBack(t *testing.T) {
	s := State{Cards: list("one"), Sel: 1}
	s, a := Step(s, press(sim.ButtonA))
	if a != Stay || s.Screen != Settings {
		t.Fatalf("A on Settings: action %v screen %v", a, s.Screen)
	}
	s, a = Step(s, press(sim.ButtonA))
	if a != Scan || s.Screen != WiFi {
		t.Fatalf("A on WI-FI: action %v screen %v", a, s.Screen)
	}
	if _, a := Step(s, press(sim.ButtonSelect)); a != Scan {
		t.Fatalf("Start on the networks: action %v, want a search", a)
	}
	s, _ = Step(s, press(sim.ButtonB))
	if s.Screen != Settings {
		t.Fatalf("B on the networks: screen %v", s.Screen)
	}
	s, _ = Step(s, press(sim.ButtonCancel))
	if s.Screen != Games || s.Sel != 1 {
		t.Fatalf("Cancel on the settings: screen %v selection %d", s.Screen, s.Sel)
	}
}

func networks() wifi.Status {
	return wifi.Status{State: wifi.Disconnected, Networks: []wifi.Network{
		{SSID: "Home", Signal: -50, Security: wifi.PSK},
		{SSID: "Cafe", Signal: -60, Security: wifi.Open},
		{SSID: "Office", Signal: -70, Security: wifi.Unsupported},
		{SSID: "Known", Signal: -80, Security: wifi.PSK, Saved: true},
	}}
}

// TestStepChooseNetwork: a network with a password asks for it; an open one, or one joined
// before, is joined at once; one the console cannot join says so.
func TestStepChooseNetwork(t *testing.T) {
	s := State{Screen: WiFi, WiFi: networks()}
	got, a := Step(s, press(sim.ButtonA))
	if a != Stay || got.Screen != Password || got.Target.SSID != "Home" {
		t.Fatalf("A on Home: action %v screen %v target %q", a, got.Screen, got.Target.SSID)
	}
	for sel, ssid := range map[int]string{1: "Cafe", 3: "Known"} {
		s.NetSel = sel
		got, a := Step(s, press(sim.ButtonA))
		if a != Join || got.Target.SSID != ssid || got.Keys.Text != "" {
			t.Fatalf("A on %s: action %v target %q", ssid, a, got.Target.SSID)
		}
	}
	s.NetSel = 2
	if got, a := Step(s, press(sim.ButtonA)); a != Stay || got.Screen != WiFi || got.Notice == "" {
		t.Fatalf("A on Office: action %v screen %v notice %q", a, got.Screen, got.Notice)
	}
}

// TestStepPassword types a password on the keyboard: characters, a capital, a symbol, a
// deletion; too short a password is refused; Start joins.
func TestStepPassword(t *testing.T) {
	s := State{Screen: Password, Target: wifi.Network{SSID: "Home", Security: wifi.PSK}}
	steps := func(bs ...sim.Button) {
		for _, b := range bs {
			s, _ = Step(s, press(b))
		}
	}
	// "1" is under the cursor at the start; Down reaches "q".
	steps(sim.ButtonA, sim.ButtonDown, sim.ButtonA)
	if s.Keys.Text != "1q" {
		t.Fatalf("typed %q", s.Keys.Text)
	}
	// SHIFT is on the action row, under the first two columns.
	steps(sim.ButtonDown, sim.ButtonDown, sim.ButtonDown, sim.ButtonA)
	if s.Keys.Row != actionRow || s.Keys.Col != 0 || s.Keys.Page != 1 {
		t.Fatalf("after SHIFT: row %d col %d page %d", s.Keys.Row, s.Keys.Col, s.Keys.Page)
	}
	steps(sim.ButtonUp, sim.ButtonA) // the row above SHIFT: "Z"
	if s.Keys.Text != "1qZ" {
		t.Fatalf("typed %q", s.Keys.Text)
	}
	steps(sim.ButtonB)
	if s.Keys.Text != "1q" {
		t.Fatalf("B did not delete: %q", s.Keys.Text)
	}
	if got, a := Step(s, press(sim.ButtonSelect)); a != Stay || got.Notice == "" {
		t.Fatalf("Start on a short password: action %v notice %q", a, got.Notice)
	}
	s.Keys.Text = "password"
	got, a := Step(s, press(sim.ButtonSelect))
	if a != Join || got.Screen != WiFi {
		t.Fatalf("Start: action %v screen %v", a, got.Screen)
	}
	// B on an empty password goes back.
	s.Keys.Text = ""
	if got, _ := Step(s, press(sim.ButtonB)); got.Screen != WiFi {
		t.Fatalf("B on nothing typed: screen %v", got.Screen)
	}
}

// TestKeyboardReachesEveryCharacter: every printable ASCII character is on a page.
func TestKeyboardReachesEveryCharacter(t *testing.T) {
	have := map[rune]bool{' ': true} // SPACE is an action
	for _, page := range keyPages {
		for _, row := range page {
			for _, r := range row {
				have[r] = true
			}
		}
	}
	for r := rune(32); r < 127; r++ {
		if !have[r] {
			t.Errorf("%q cannot be typed", r)
		}
	}
}

// TestStepChannelAndUpdates: A on the channel switches it and asks for it to be kept; A on
// INSTALL UPDATES opens the updates and asks for a look.
func TestStepChannelAndUpdates(t *testing.T) {
	s := State{Screen: Settings, SetSel: 1, Channel: update.Beta}
	s, a := Step(s, press(sim.ButtonA))
	if a != SetChannel || s.Channel != update.Stable {
		t.Fatalf("A on the channel: action %v channel %s", a, s.Channel)
	}
	s, _ = Step(s, press(sim.ButtonDown))
	s, a = Step(s, press(sim.ButtonA))
	if a != CheckUpdate || s.Screen != Updates {
		t.Fatalf("A on INSTALL UPDATES: action %v screen %v", a, s.Screen)
	}
}

// TestStepUpdates: B stops a download but not an install; a finished install restarts the
// console; a failed look can be tried again.
func TestStepUpdates(t *testing.T) {
	for _, c := range []struct {
		state  update.State
		button sim.Button
		action Action
		screen Screen
	}{
		{update.Downloading, sim.ButtonB, CancelUpdate, Updates},
		{update.Checking, sim.ButtonCancel, CancelUpdate, Updates},
		{update.Installing, sim.ButtonB, Stay, Updates},
		{update.Installed, sim.ButtonB, Stay, Updates},
		{update.UpToDate, sim.ButtonB, Stay, Settings},
		{update.Failed, sim.ButtonA, CheckUpdate, Updates},
		{update.UpToDate, sim.ButtonA, CheckUpdate, Updates},
		{update.Downloading, sim.ButtonA, Stay, Updates},
	} {
		s := State{Screen: Updates, Update: update.Status{State: c.state}}
		got, a := Step(s, press(c.button))
		if a != c.action || got.Screen != c.screen {
			t.Errorf("%d, %s: action %v screen %v, want %v %v", c.state, c.button, a, got.Screen, c.action, c.screen)
		}
	}
	if _, a := Step(State{Screen: Updates, Update: update.Status{State: update.Restart}}, press()); a != Reboot {
		t.Fatalf("an installed update: action %v, want a restart", a)
	}
}
