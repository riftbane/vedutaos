package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/riftbane/vedutaos/provision"
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

// bootPartition is what a PC sees of a Raspberry Pi OS card just written by Imager.
func bootPartition(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{"config.txt", "cmdline.txt", "user-data", "meta-data"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "provision", "testdata", "stock", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
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

func TestPiCard(t *testing.T) {
	boot := bootPartition(t)
	stockConfig := read(t, boot, "config.txt")
	vshell := fakeProgram(t, filepath.Join(t.TempDir(), "vshell"), elf.EM_AARCH64)
	key := filepath.Join(t.TempDir(), "id_ed25519.pub")
	os.WriteFile(key, []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ5f me@pc\n"), 0o644)

	code, out, errs := runTool(t, "card", boot, "--vshell", vshell, "--game", game(t, "gems", elf.EM_AARCH64), "--ssh-key", key)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, f := range []string{"vedutaos/vshell", "vedutaos/env", "vedutaos/vedutaos-ili9341.bin", "userconf.txt", "games/gems/game"} {
		if !exists(boot, f) {
			t.Errorf("no %s on the card", f)
		}
	}
	if ud := read(t, boot, "user-data"); !provision.Replaceable([]byte(ud)) || !strings.Contains(ud, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ5f me@pc") {
		t.Errorf("user-data:\n%s", ud)
	}
	if c := read(t, boot, "config.txt"); !strings.HasPrefix(c, stockConfig) || !strings.Contains(c, "dtoverlay=mipi-dbi-spi") {
		t.Errorf("config.txt:\n%s", c)
	}
	if !strings.Contains(read(t, boot, "cmdline.txt"), "vt.global_cursor_default=0") {
		t.Error("cmdline.txt keeps the cursor")
	}
	if !strings.Contains(out, "GEMS") || !strings.Contains(out, "games/gems") || !strings.Contains(out, "ILI9341") {
		t.Errorf("output:\n%s", out)
	}
	firstMeta := read(t, boot, "meta-data")

	// The same card written again is the same card: no second setup on the next start.
	if code, _, errs := runTool(t, "card", boot, "--vshell", vshell, "--ssh-key", key); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if read(t, boot, "meta-data") != firstMeta {
		t.Error("an unchanged card would be set up again")
	}
	if !exists(boot, "games/gems/game") {
		t.Error("writing the card again lost a game")
	}

	// Taking the panel away takes all of it away, and the Pi is set up again.
	if code, _, errs := runTool(t, "card", "--panel", "none", boot, "--vshell", vshell); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if exists(boot, "vedutaos/vedutaos-ili9341.bin") || read(t, boot, "config.txt") != stockConfig {
		t.Error("the panel's firmware or overlay is still on the card")
	}
	if read(t, boot, "meta-data") == firstMeta {
		t.Error("a changed card would not be set up again")
	}
}

func TestPiCardRefusesWhatItShouldNotTouch(t *testing.T) {
	vshell := fakeProgram(t, filepath.Join(t.TempDir(), "vshell"), elf.EM_AARCH64)
	if code, _, errs := runTool(t, "card", t.TempDir(), "--vshell", vshell); code != 1 || !strings.Contains(errs, "not the boot partition") {
		t.Errorf("a folder that is not a boot partition: exit %d, %s", code, errs)
	}

	boot := bootPartition(t)
	imager := "#cloud-config\nhostname: mypi\nusers:\n- name: me\n"
	os.WriteFile(filepath.Join(boot, "user-data"), []byte(imager), 0o644)
	if code, _, errs := runTool(t, "card", boot, "--vshell", vshell); code != 1 || !strings.Contains(errs, "--replace-user-data") {
		t.Errorf("Imager's user-data: exit %d, %s", code, errs)
	}
	if read(t, boot, "user-data") != imager {
		t.Error("Imager's user-data was overwritten")
	}
	if code, _, errs := runTool(t, "card", boot, "--vshell", vshell, "--replace-user-data"); code != 0 {
		t.Errorf("with --replace-user-data: exit %d, %s", code, errs)
	}

	amd64 := fakeProgram(t, filepath.Join(t.TempDir(), "vshell"), elf.EM_X86_64)
	if code, _, errs := runTool(t, "card", bootPartition(t), "--vshell", amd64); code != 1 || !strings.Contains(errs, "linux/arm64") {
		t.Errorf("an amd64 dashboard: exit %d, %s", code, errs)
	}
}

func TestQEMUCard(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "card")
	vshell := fakeProgram(t, filepath.Join(t.TempDir(), "vshell"), elf.EM_AARCH64)
	code, out, errs := runTool(t, "card", "--target", "qemu", dir, "--vshell", vshell, "--game", game(t, "pc", elf.EM_X86_64))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, f := range []string{"config.txt", "cmdline.txt", "userconf.txt", "vedutaos/vedutaos-ili9341.bin"} {
		if exists(dir, f) {
			t.Errorf("%s on a card for QEMU", f)
		}
	}
	if !strings.Contains(read(t, dir, "vedutaos/env"), "VEDUTA_SCALE=4\n") {
		t.Error("QEMU's card does not draw at a quarter of the emulated screen")
	}
	if !strings.Contains(out, "built for amd64") {
		t.Errorf("a game the console cannot run is not pointed out:\n%s", out)
	}
}

// Run from its source tree with no dashboard at hand, the tool builds one.
func TestDashboardBuiltFromSource(t *testing.T) {
	var root string
	saved := buildVShell
	defer func() { buildVShell = saved }()
	buildVShell = func(r, out string) error {
		root = r
		fakeProgram(t, out, elf.EM_AARCH64)
		return nil
	}
	dir := t.TempDir()
	if code, _, errs := runTool(t, "card", "--target", "qemu", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if _, err := os.Stat(filepath.Join(root, "cmd", "vshell", "main.go")); err != nil {
		t.Errorf("built from %q, which is not the source tree", root)
	}
	if !exists(dir, "vedutaos/vshell") {
		t.Error("no dashboard on the card")
	}
}

func TestParsePins(t *testing.T) {
	got, err := parsePins("dc=22, backlight=none", provision.DefaultPins)
	if err != nil || got != (provision.Pins{DC: 22, Reset: 25, Backlight: -1}) {
		t.Errorf("%+v %v", got, err)
	}
	for _, bad := range []string{"dc", "dc=x", "cs=8", "reset=-3"} {
		if _, err := parsePins(bad, provision.DefaultPins); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestUsage(t *testing.T) {
	if code, _, _ := runTool(t); code != 2 {
		t.Error("no command")
	}
	if code, _, _ := runTool(t, "flash"); code != 2 {
		t.Error("unknown command")
	}
	if code, _, _ := runTool(t, "card"); code != 2 {
		t.Error("card without a folder")
	}
	if code, _, errs := runTool(t, "card", t.TempDir(), "--scale", "9", "--vshell", "x"); code != 2 || !strings.Contains(errs, "scale") {
		t.Errorf("scale 9: %d %s", code, errs)
	}
	boot := bootPartition(t)
	if code, _, errs := runTool(t, "card", boot, "--rotate", "180", "--vshell", "x"); code != 2 || !strings.Contains(errs, "rotate") {
		t.Errorf("rotate 180: %d %s", code, errs)
	}
	if exists(boot, "vedutaos") {
		t.Error("a refused card was written to")
	}
}
