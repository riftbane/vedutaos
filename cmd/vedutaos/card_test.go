package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/riftbane/vedutaos/image"
)

// fakeProgram writes the header of a Linux program for a machine: enough for the tool to
// tell an arm64 build from any other, and nothing that runs.
func fakeProgram(t *testing.T, path string, machine elf.Machine) string {
	t.Helper()
	var h [64]byte
	copy(h[:], []byte{0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)})
	le := binary.LittleEndian
	le.PutUint16(h[16:], uint16(elf.ET_EXEC))
	le.PutUint16(h[18:], uint16(machine))
	le.PutUint32(h[20:], uint32(elf.EV_CURRENT))
	le.PutUint16(h[52:], 64) // header size
	le.PutUint16(h[54:], 56) // program header entry size
	le.PutUint16(h[58:], 64) // section header entry size
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, h[:], 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func game(t *testing.T, name string, machine elf.Machine) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	fakeProgram(t, filepath.Join(dir, "game"), machine)
	if err := os.WriteFile(filepath.Join(dir, "card.json"), []byte(`{"veduta":"card/1","title":"`+strings.ToUpper(name)+`","exec":"game"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runTool(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
	return err == nil
}

func TestCard(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(t.TempDir(), "id_ed25519.pub")
	os.WriteFile(key, []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ5f me@pc\n"), 0o644)

	// A card of games alone: the console's own settings and dashboard stay.
	code, out, errs := runTool(t, "card", dir, "--game", game(t, "gems", elf.EM_AARCH64), "--ssh-key", key)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !exists(dir, "games/gems/game") || !exists(dir, "games/gems/card.json") {
		t.Error("the game is not on the card")
	}
	if exists(dir, "vedutaos/env") || exists(dir, "vedutaos/vshell") {
		t.Error("settings or a dashboard written without being asked")
	}
	if k := read(t, dir, "vedutaos/authorized_keys"); k != "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ5f me@pc\n" {
		t.Errorf("authorized_keys: %q", k)
	}
	if !strings.Contains(out, "GEMS") || !strings.Contains(out, "games/gems") {
		t.Errorf("output:\n%s", out)
	}

	// Written again with settings and a dashboard: the game stays, the rest is added.
	vshell := fakeProgram(t, filepath.Join(t.TempDir(), "vshell"), elf.EM_AARCH64)
	if code, out, errs = runTool(t, "card", dir, "--vshell", vshell, "--scale", "2", "--pad", "/dev/input/event3"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !exists(dir, "games/gems/game") || !exists(dir, "vedutaos/vshell") {
		t.Error("writing the card again lost the game, or did not add the dashboard")
	}
	if env := read(t, dir, "vedutaos/env"); !strings.Contains(env, "VEDUTA_SCALE=2\n") || !strings.Contains(env, "VEDUTA_PAD=/dev/input/event3\n") || strings.Contains(env, "VEDUTA_FB") {
		t.Errorf("env:\n%s", env)
	}
	if !strings.Contains(out, "replacing the image's") {
		t.Errorf("output does not mention the dashboard:\n%s", out)
	}
}

func TestCardRefusesWhatTheConsoleCannotRun(t *testing.T) {
	amd64 := fakeProgram(t, filepath.Join(t.TempDir(), "vshell"), elf.EM_X86_64)
	if code, _, errs := runTool(t, "card", t.TempDir(), "--vshell", amd64); code != 1 || !strings.Contains(errs, "linux/arm64") {
		t.Errorf("an amd64 dashboard: exit %d, %s", code, errs)
	}
	dir := t.TempDir()
	code, out, errs := runTool(t, "card", dir, "--game", game(t, "pc", elf.EM_X86_64))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(out, "built for amd64") {
		t.Errorf("an amd64 game is listed without a warning:\n%s", out)
	}
	if code, _, errs := runTool(t, "card", dir, "--ssh-key", filepath.Join(dir, "missing.pub")); code != 1 {
		t.Errorf("a missing key file: exit %d, %s", code, errs)
	}
}

func TestDashboardBuiltFromSource(t *testing.T) {
	saved := buildVShell
	defer func() { buildVShell = saved }()
	built := ""
	buildVShell = func(r, out string) error {
		built = r
		fakeProgram(t, out, elf.EM_AARCH64)
		return nil
	}
	p, cleanup, err := findVShell("", os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if root, _ := sourceRoot(); built != root || !exists(filepath.Dir(p), "vshell") {
		t.Errorf("built from %q, got %q", built, p)
	}
	if p, _, _ := findVShell("/x/vshell", os.Stderr); p != "/x/vshell" {
		t.Errorf("--vshell ignored: %q", p)
	}
}

func TestParsePins(t *testing.T) {
	got, err := parsePins("dc=22,reset=none,backlight=none", image.DefaultPins)
	if err != nil || got != (image.Pins{DC: 22, Reset: -1, Backlight: -1}) {
		t.Errorf("%+v %v", got, err)
	}
	for _, bad := range []string{"dc=none", "cs=8", "dc=x", "dc"} {
		if _, err := parsePins(bad, image.DefaultPins); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestUsage(t *testing.T) {
	if code, _, _ := runTool(t); code != 2 {
		t.Error("no command should fail")
	}
	if code, _, _ := runTool(t, "burn"); code != 2 {
		t.Error("an unknown command should fail")
	}
	if code, _, _ := runTool(t, "flash"); code != 2 {
		t.Error("flash without a device should fail")
	}
	if code, out, _ := runTool(t, "version"); code != 0 || out != "vedutaos dev\n" {
		t.Errorf("version: %d %q", code, out)
	}
	if code, _, _ := runTool(t, "card"); code != 2 {
		t.Error("card without a folder should fail")
	}
	if code, _, errs := runTool(t, "card", t.TempDir(), "--scale", "9"); code != 2 || !strings.Contains(errs, "scale") {
		t.Errorf("scale 9: %d %s", code, errs)
	}
}
