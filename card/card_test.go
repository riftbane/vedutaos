package card

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// games builds a card's games directory. Each entry is "folder|card.json contents|files",
// where files are comma-separated names to create empty.
func games(t *testing.T, entries ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, e := range entries {
		parts := strings.SplitN(e, "|", 3)
		dir := filepath.Join(root, parts[0])
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if len(parts) > 1 && parts[1] != "" {
			if err := os.WriteFile(filepath.Join(dir, File), []byte(parts[1]), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if len(parts) > 2 {
			for _, f := range strings.Split(parts[2], ",") {
				if f == "" {
					continue
				}
				if err := os.WriteFile(filepath.Join(dir, f), []byte("binary"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return root
}

const good = `{"veduta":"card/1","title":"Cave of Gems","name":"gems","version":"v1.2.0","exec":"game","icon":"icon.png"}`

func TestScan(t *testing.T) {
	root := games(t,
		"gems|"+good+"|game,icon.png",
		"zzz|{\"veduta\":\"card/1\",\"title\":\"Another\"}|game",
		"nocard||game", // no description at all: still a game
	)
	cards, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 3 {
		t.Fatalf("found %d games: %+v", len(cards), cards)
	}
	// Sorted by title: "Another", "Cave of Gems", "nocard".
	if cards[0].Title != "Another" || cards[1].Title != "Cave of Gems" || cards[2].Title != "nocard" {
		t.Fatalf("order: %q, %q, %q", cards[0].Title, cards[1].Title, cards[2].Title)
	}
	g := cards[1]
	if g.Name != "gems" || g.Version != "v1.2.0" || g.Problem != "" {
		t.Errorf("described game: %+v", g)
	}
	if filepath.Base(g.Exec) != "game" || filepath.Base(g.Icon) != "icon.png" {
		t.Errorf("exec %q icon %q", g.Exec, g.Icon)
	}
	// A folder without a description is shown by its folder name and has no icon.
	if cards[2].Icon != "" || cards[2].Problem == "" {
		t.Errorf("undescribed game: %+v", cards[2])
	}
}

// TestScanKeepsBrokenDescriptions: a damaged card must never hide a game that runs.
func TestScanKeepsBrokenDescriptions(t *testing.T) {
	for _, c := range []struct{ name, body, problem string }{
		{"broken json", `{"veduta":"card/1",`, "card.json"},
		{"another format", `{"veduta":"card/2","title":"Future"}`, "is not card/1"},
		{"unknown field", `{"veduta":"card/1","title":"X","extra":1}`, "unknown field"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := games(t, "game1|"+c.body+"|game")
			cards, err := Scan(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(cards) != 1 {
				t.Fatalf("found %d games", len(cards))
			}
			if cards[0].Title != "game1" || cards[0].Exec == "" {
				t.Errorf("card: %+v", cards[0])
			}
			if !strings.Contains(cards[0].Problem, c.problem) {
				t.Errorf("problem %q, want one mentioning %q", cards[0].Problem, c.problem)
			}
		})
	}
}

// TestScanSkipsFoldersWithNothingToRun: a folder that cannot be launched is not a game.
func TestScanSkipsFoldersWithNothingToRun(t *testing.T) {
	root := games(t, "empty|"+good+"|", "readme||notes.txt")
	cards, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 0 {
		t.Fatalf("listed %+v", cards)
	}
	// Files beside the folders are ignored rather than mistaken for games.
	os.WriteFile(filepath.Join(root, "loose.txt"), []byte("x"), 0o644)
	if cards, _ = Scan(root); len(cards) != 0 {
		t.Fatalf("a loose file was listed: %+v", cards)
	}
}

// TestScanPrefersThisArchitecture: one card serves boards of different architectures.
func TestScanPrefersThisArchitecture(t *testing.T) {
	root := games(t, "multi|"+good+"|game,game-"+runtime.GOARCH)
	cards, err := Scan(root)
	if err != nil || len(cards) != 1 {
		t.Fatalf("scan: %+v %v", cards, err)
	}
	// The description names "game", and what it names wins: it is the game's own choice.
	if filepath.Base(cards[0].Exec) != "game" {
		t.Errorf("chose %q", cards[0].Exec)
	}
	// Without a description, the build for this machine is preferred.
	root = games(t, "multi||game,game-"+runtime.GOARCH)
	cards, _ = Scan(root)
	if len(cards) != 1 || filepath.Base(cards[0].Exec) != "game-"+runtime.GOARCH {
		t.Fatalf("chose %+v", cards)
	}
}

// TestScanRefusesToLeaveTheFolder: a card comes from a card, which comes from anywhere.
func TestScanRefusesToLeaveTheFolder(t *testing.T) {
	root := games(t, `escape|{"veduta":"card/1","title":"Escape","exec":"../../bin/sh","icon":"/etc/passwd"}|game`)
	cards, err := Scan(root)
	if err != nil || len(cards) != 1 {
		t.Fatalf("scan: %+v %v", cards, err)
	}
	if filepath.Base(cards[0].Exec) != "game" {
		t.Errorf("exec %q, want the game in the folder", cards[0].Exec)
	}
	if cards[0].Icon != "" {
		t.Errorf("icon %q, want none", cards[0].Icon)
	}
}

func TestScanMissingDirectory(t *testing.T) {
	if _, err := Scan(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("a missing games directory was accepted")
	}
}
