// Package card reads the games present on a console's card.
//
// A game is a folder that a person drags onto the card from a PC. Beside the engine's own
// veduta.json it carries a card.json, a small frozen description the console reads:
//
//	{"veduta": "card/1", "title": "Cave of Gems", "name": "gems",
//	 "version": "v1.2.0", "exec": "game", "icon": "icon.png"}
//
// It is deliberately not the engine's manifest. That one is strict and grows fields with
// the engine, so a console built today could not read a manifest written by a newer
// engine; this one never changes, so a console can still list a game written years
// earlier. And a folder whose description is missing or damaged is still listed and still
// launched if something in it can run: never hide a game that could be played.
package card

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Format is the value of the "veduta" field of a card this package understands.
const Format = "card/1"

// File is the description a game folder carries.
const File = "card.json"

// Card is one game on the card, ready to be shown and launched.
type Card struct {
	Dir     string // the game's folder
	Title   string // what the dashboard shows; the folder name when nothing better is known
	Name    string // identifier from the description, empty when there is none
	Version string // version from the description, empty when there is none
	Exec    string // the executable to run
	Icon    string // the picture to show, empty when the folder has none
	Problem string // why the description was not used, empty when all is well
}

// source is card.json as it is written.
type source struct {
	Veduta  string `json:"veduta"`
	Title   string `json:"title"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Exec    string `json:"exec"`
	Icon    string `json:"icon"`
}

// Scan returns the games in a directory, sorted by title and then by folder so the
// dashboard lists them in the same order every time. A folder with nothing runnable in it
// is not a game and is passed over; anything else is returned, with Problem saying what
// was wrong when its description could not be used.
func Scan(dir string) ([]Card, error) {
	return ScanFor(dir, runtime.GOARCH)
}

// ScanFor is Scan as a machine of another architecture sees the card: a PC preparing a
// card uses it to list what the console will list.
func ScanFor(dir, goarch string) ([]Card, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("card: %w", err)
	}
	var out []Card
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if c, ok := load(filepath.Join(dir, e.Name()), e.Name(), goarch); ok {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].Dir < out[j].Dir
	})
	return out, nil
}

// load describes one folder. It reports false when the folder holds nothing that can run.
func load(dir, base, goarch string) (Card, bool) {
	c := Card{Dir: dir, Title: base}
	src, err := read(filepath.Join(dir, File))
	switch {
	case err != nil:
		c.Problem = err.Error()
	default:
		if src.Title != "" {
			c.Title = src.Title
		}
		c.Name, c.Version = src.Name, src.Version
	}
	if exec, ok := findExec(dir, src.Exec, goarch); ok {
		c.Exec = exec
	} else {
		return Card{}, false // nothing to launch: this is not a game
	}
	if icon, ok := findIcon(dir, src.Icon); ok {
		c.Icon = icon
	}
	return c, true
}

// read parses a card description. Unknown fields are refused so that a card written for a
// later format is reported rather than half-understood.
func read(path string) (source, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return source{}, fmt.Errorf("no %s", File)
		}
		return source{}, err
	}
	var src source
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&src); err != nil {
		return source{}, fmt.Errorf("%s: %v", File, err)
	}
	if src.Veduta != Format {
		return source{}, fmt.Errorf("%s: %q is not %s", File, src.Veduta, Format)
	}
	return src, nil
}

// findExec picks the program to run: the one the description names, else the build for
// the machine's architecture, else a plain "game". One card therefore serves boards of
// different architectures.
func findExec(dir, named, goarch string) (string, bool) {
	names := []string{"game-" + goarch, "game"}
	if named != "" && safeName(named) {
		names = append([]string{named}, names...)
	}
	for _, n := range names {
		p := filepath.Join(dir, n)
		if runnable(p) {
			return p, true
		}
	}
	return "", false
}

func findIcon(dir, named string) (string, bool) {
	names := []string{"icon.png"}
	if named != "" && safeName(named) {
		names = append([]string{named}, names...)
	}
	for _, n := range names {
		p := filepath.Join(dir, n)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, true
		}
	}
	return "", false
}

// safeName keeps a description from pointing outside its own folder: a card comes from a
// card, and a card comes from anywhere.
func safeName(s string) bool {
	return s != "" && !strings.ContainsAny(s, `/\`) && s != "." && s != ".."
}

// runnable reports whether a path is a file this machine can execute. A card formatted for
// a PC carries no permission bits, so a file that is there but not marked executable still
// counts: the launcher makes it executable when it has to.
func runnable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
