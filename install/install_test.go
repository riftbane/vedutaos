package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/riftbane/vedutaos/card"
)

// write creates files under root; each entry is "path=contents".
func write(t *testing.T, root string, entries ...string) {
	t.Helper()
	for _, e := range entries {
		name, contents, _ := strings.Cut(e, "=")
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(contents), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// tree lists every file under root with its contents, as "path=contents".
func tree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel)+"="+string(b))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func same(t *testing.T, got, want []string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("files:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func noBuild(dir, entry, out string) error {
	return errors.New("nothing should be built")
}

type tarEntry struct {
	name, body string
	kind       byte
}

func tarGz(t *testing.T, path string, entries ...tarEntry) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		kind := e.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: kind}
		if kind != tar.TypeReg {
			h.Size = 0
			h.Linkname = "/etc/passwd"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	gz.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFolderIsCopiedAndReplacesItsOldSelf(t *testing.T) {
	src := filepath.Join(t.TempDir(), "gems")
	write(t, src, "card.json={}", "game-arm64=v2", "assets/a.json=a")
	games := t.TempDir()
	write(t, games, "gems/old-file=stale", "other/game=untouched")
	dst, err := Game(games, src, noBuild)
	if err != nil {
		t.Fatal(err)
	}
	if dst != filepath.Join(games, "gems") {
		t.Errorf("installed at %s", dst)
	}
	same(t, tree(t, games), []string{"gems/card.json={}", "gems/game-arm64=v2", "gems/assets/a.json=a", "other/game=untouched"})
}

// A release archive unpacks to the folder it names, exactly as the release workflow built it.
func TestReleaseArchive(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "demo_v1.0.0_linux_arm64.tar.gz")
	tarGz(t, archive,
		tarEntry{name: "demo/", kind: tar.TypeDir},
		tarEntry{name: "demo/demo", body: "elf"},
		tarEntry{name: "demo/card.json", body: `{"veduta":"card/1"}`},
		tarEntry{name: "demo/assets/scenes/main.json", body: "{}"},
	)
	games := t.TempDir()
	if _, err := Game(games, archive, noBuild); err != nil {
		t.Fatal(err)
	}
	same(t, tree(t, games), []string{"demo/demo=elf", `demo/card.json={"veduta":"card/1"}`, "demo/assets/scenes/main.json={}"})
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(filepath.Join(games, "demo", "demo")); fi.Mode().Perm()&0o100 == 0 {
			t.Error("the program lost its executable bit")
		}
	}
}

// An archive comes from anywhere: nothing in it may land outside the game's own folder, and a
// refused archive leaves the card as it was.
func TestArchiveThatWouldEscape(t *testing.T) {
	cases := map[string][]tarEntry{
		"parent":   {{name: "demo/game", body: "x"}, {name: "demo/../../evil", body: "x"}},
		"absolute": {{name: "/etc/evil", body: "x"}},
		"two tops": {{name: "demo/game", body: "x"}, {name: "other/game", body: "x"}},
		"symlink":  {{name: "demo/game", body: "x"}, {name: "demo/passwd", kind: tar.TypeSymlink}},
		"drive":    {{name: "C:/evil", body: "x"}},
		"empty":    {},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "bad.tar.gz")
			tarGz(t, archive, entries...)
			games := filepath.Join(dir, "games")
			write(t, games, "demo/game=kept")
			if _, err := Game(games, archive, noBuild); err == nil {
				t.Fatal("installed")
			}
			same(t, tree(t, games), []string{"demo/game=kept"})
			if _, err := os.Stat(filepath.Join(dir, "evil")); err == nil {
				t.Fatal("a file escaped")
			}
		})
	}
}

func TestZip(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "gems.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"gems/game-arm64": "elf", "gems/card.json": "{}"} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	os.WriteFile(archive, buf.Bytes(), 0o644)
	games := t.TempDir()
	if _, err := Game(games, archive, noBuild); err != nil {
		t.Fatal(err)
	}
	same(t, tree(t, games), []string{"gems/game-arm64=elf", "gems/card.json={}"})

	bad := filepath.Join(t.TempDir(), "bad.zip")
	buf.Reset()
	zw = zip.NewWriter(&buf)
	w, _ := zw.Create("gems/../../evil")
	w.Write([]byte("x"))
	zw.Close()
	os.WriteFile(bad, buf.Bytes(), 0o644)
	if _, err := Game(t.TempDir(), bad, noBuild); err == nil {
		t.Fatal("an escaping zip was installed")
	}
}

// fakeBuild records what it was asked to build and writes a stand-in program.
type fakeBuild struct{ dir, entry, out string }

func (f *fakeBuild) build(dir, entry, out string) error {
	f.dir, f.entry, f.out = dir, entry, out
	return os.WriteFile(out, []byte("built"), 0o755)
}

// A project is staged as its release workflow stages it: the program named after the game,
// its manifest, README, card and icon, and its assets without the cooked ones.
func TestProject(t *testing.T) {
	proj := t.TempDir()
	write(t, proj,
		`veduta.json={"veduta":"project/1","name":"demo","entry":"./cmd/game","icon":"art/icon.png","assets":"assets","cooked":"assets/.cooked","tick_rate":20}`,
		"cmd/game/main.go=package main",
		"assets/scenes/main.json={}",
		"assets/.cooked/main.vda=cooked",
		"art/icon.png=png",
		"README.md=readme",
	)
	var fb fakeBuild
	games := t.TempDir()
	if _, err := Game(games, proj, fb.build); err != nil {
		t.Fatal(err)
	}
	if fb.dir != proj || fb.entry != "./cmd/game" || filepath.Base(fb.out) != "demo" {
		t.Errorf("built %+v", fb)
	}
	files := tree(t, games)
	var names []string
	for _, f := range files {
		name, _, _ := strings.Cut(f, "=")
		names = append(names, name)
	}
	same(t, names, []string{"demo/demo", "demo/veduta.json", "demo/README.md", "demo/card.json", "demo/icon.png", "demo/assets/scenes/main.json"})

	cards, err := card.ScanFor(games, "arm64")
	if err != nil || len(cards) != 1 {
		t.Fatalf("the console would list %+v (%v)", cards, err)
	}
	c := cards[0]
	if c.Problem != "" || c.Title != "demo" || filepath.Base(c.Exec) != "demo" || filepath.Base(c.Icon) != "icon.png" {
		t.Errorf("listed as %+v", c)
	}
}

// A Lua project is staged without a build: its scripts where they are, its manifest, card,
// icon and assets. Staging the result again gives the same folder.
func TestScriptProject(t *testing.T) {
	proj := t.TempDir()
	write(t, proj,
		`veduta.json={"veduta":"project/1","name":"lua","engine":"v2.0.0","script":"main.lua","icon":"icon.png"}`,
		"main.lua=game = {}",
		"lib/util.lua=return {}",
		"out/tmp.lua=junk",
		".git/hook.lua=junk",
		"tests/scenarios/start.scenario.json={}",
		"assets/scenes/main.json={}",
		"assets/.cooked/main.vda=cooked",
		"icon.png=png",
		`card.json={"veduta":"card/1","title":"Lua","name":"lua","exec":"lua"}`,
	)
	games := t.TempDir()
	build := func(string, string, string) error { t.Error("a Lua game was built"); return nil }
	if _, err := Game(games, proj, build); err != nil {
		t.Fatal(err)
	}
	want := []string{"lua/assets/scenes/main.json={}", "lua/card.json", "lua/icon.png=png", "lua/lib/util.lua=return {}", "lua/main.lua=game = {}", "lua/veduta.json"}
	check := func(what string) {
		t.Helper()
		var got []string
		for _, f := range tree(t, games) {
			if name, _, _ := strings.Cut(f, "="); name == "lua/card.json" || name == "lua/veduta.json" {
				f = name
			}
			got = append(got, f)
		}
		sort.Strings(got)
		same(t, got, want)
		cards, err := card.ScanFor(games, "arm64")
		if err != nil || len(cards) != 1 || cards[0].Problem != "" || cards[0].Title != "Lua" || filepath.Base(cards[0].Script) != "main.lua" {
			t.Fatalf("%s: the console would list %+v (%v)", what, cards, err)
		}
	}
	check("the project")
	again := t.TempDir()
	if err := os.Rename(filepath.Join(games, "lua"), filepath.Join(again, "lua")); err != nil {
		t.Fatal(err)
	}
	if _, err := Game(games, filepath.Join(again, "lua"), build); err != nil {
		t.Fatal(err)
	}
	check("the game folder")
}

func TestProjectKeepsItsOwnCard(t *testing.T) {
	proj := t.TempDir()
	write(t, proj,
		`veduta.json={"name":"gems","entry":"cmd/game"}`,
		"cmd/game/main.go=package main",
		`card.json={"veduta":"card/1","title":"Cave of Gems","name":"gems"}`,
	)
	var fb fakeBuild
	games := t.TempDir()
	if _, err := Game(games, proj, fb.build); err != nil {
		t.Fatal(err)
	}
	var desc map[string]any
	b, _ := os.ReadFile(filepath.Join(games, "gems", "card.json"))
	if err := json.Unmarshal(b, &desc); err != nil {
		t.Fatal(err)
	}
	if desc["title"] != "Cave of Gems" || desc["exec"] != "gems" {
		t.Errorf("card %v", desc)
	}
	if fb.entry != "./cmd/game" {
		t.Errorf("entry %q: go build needs a leading ./ to read it as a folder", fb.entry)
	}

	write(t, proj, `card.json={"veduta":"card/2"}`)
	if _, err := Game(games, proj, fb.build); err == nil {
		t.Error("a card the console cannot read was installed")
	}
}

func TestFailedBuildLeavesTheOldGame(t *testing.T) {
	proj := t.TempDir()
	write(t, proj, `veduta.json={"name":"demo","entry":"./cmd/game"}`, "cmd/game/main.go=package main")
	games := t.TempDir()
	write(t, games, "demo/demo=old")
	if _, err := Game(games, proj, func(string, string, string) error { return errors.New("compile error") }); err == nil {
		t.Fatal("installed")
	}
	same(t, tree(t, games), []string{"demo/demo=old"})
}

func TestNotAGame(t *testing.T) {
	f := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(f, []byte("hello"), 0o644)
	if _, err := Game(t.TempDir(), f, noBuild); err == nil {
		t.Error("a text file was installed")
	}
	if _, err := Game(t.TempDir(), filepath.Join(t.TempDir(), "missing"), noBuild); err == nil {
		t.Error("a missing folder was installed")
	}
}

func TestArch(t *testing.T) {
	f := filepath.Join(t.TempDir(), "script")
	os.WriteFile(f, []byte("#!/bin/sh\n"), 0o755)
	if _, err := Arch(f); err == nil {
		t.Error("a shell script passed for a program")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the test binary is a Linux program only on Linux")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Arch(self); err != nil || got != runtime.GOARCH {
		t.Errorf("this test is %q (%v), want %q", got, err, runtime.GOARCH)
	}
}
