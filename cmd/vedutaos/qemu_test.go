package main

import (
	"debug/elf"
	"encoding/binary"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update-golden", false, "rewrite the reference command lines")

// The QEMU command line is pinned: each part of it was found necessary on a real run (see
// docs/quickstart-qemu.md), and a change should be tried on one before it is accepted.
func TestQEMUArgsGolden(t *testing.T) {
	cases := map[string]qemuConfig{
		"qemu-linux.txt": {
			Kernel: "/src/vedutaos/out/vmlinuz", Initrd: "/src/vedutaos/out/initrd.img", Root: "4d8fd085-02",
			Disk: "/home/me/.cache/vedutaos/qemu/console.qcow2", Card: "/home/me/.cache/vedutaos/qemu/card",
			Accel: "tcg", CPUs: 4, Memory: "1G", SSHPort: 2222,
		},
		"qemu-windows.txt": {
			Kernel: `C:\vedutaos\vmlinuz`, Initrd: `C:\vedutaos\initrd.img`, Root: "4d8fd085-02",
			Disk: `C:\Users\Me\AppData\Local\vedutaos\qemu\console.qcow2`, Card: `D:\cards\one,two`,
			Accel: "tcg", CPUs: 4, Memory: "1G", SSHPort: 2223, Display: "sdl",
			Extra: []string{"-monitor", "tcp:127.0.0.1:4444,server,nowait"},
		},
		"qemu-kvm.txt": {
			Kernel: "/i/vmlinuz", Initrd: "/i/initrd.img", Root: "0000abcd-02", Disk: "/d.qcow2", Card: "/card",
			Accel: "kvm", CPUs: 2, Memory: "1G", SSHPort: 2222,
		},
	}
	for name, c := range cases {
		got := strings.Join(qemuArgs(c), "\n") + "\n"
		path := filepath.Join("testdata", name)
		if *update {
			os.MkdirAll("testdata", 0o755)
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (run with -update-golden once the command is right)", err)
		}
		if got != string(want) {
			t.Errorf("%s differs (run with -update-golden once it is right):\n%s", name, got)
		}
	}
}

func TestQEMUEscapesCommas(t *testing.T) {
	args := strings.Join(qemuArgs(qemuConfig{Card: "/a,b", Disk: "/c,d", CPUs: 1, Memory: "1G"}), " ")
	if !strings.Contains(args, "dir=/a,,b,label=VEDUTA") || !strings.Contains(args, "file.filename=/c,,d ") {
		t.Fatal(args)
	}
}

// fakeImage writes a disk image's first sector with the identifier the kernel names the
// root partition by.
func fakeImage(t *testing.T, dir string, id uint32) string {
	t.Helper()
	var mbr [512]byte
	binary.LittleEndian.PutUint32(mbr[0x1b8:], id)
	mbr[510], mbr[511] = 0x55, 0xaa
	p := filepath.Join(dir, "vedutaos.img")
	if err := os.WriteFile(p, mbr[:], 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"vmlinuz", "initrd.img"} {
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	return p
}

func TestRootPartUUID(t *testing.T) {
	img := fakeImage(t, t.TempDir(), 0x4d8fd085)
	if got, err := rootPartUUID(img); err != nil || got != "4d8fd085-02" {
		t.Errorf("%q %v", got, err)
	}
	short := filepath.Join(t.TempDir(), "short.img")
	os.WriteFile(short, []byte("not an image"), 0o644)
	if _, err := rootPartUUID(short); err == nil {
		t.Error("a file that is not an image was accepted")
	}
	blank := filepath.Join(t.TempDir(), "blank.img")
	os.WriteFile(blank, make([]byte, 512), 0o644)
	if _, err := rootPartUUID(blank); err == nil {
		t.Error("a sector without an MBR was accepted")
	}
}

func withHost(t *testing.T, os_, arch string, files ...string) {
	t.Helper()
	savedOS, savedArch, savedOK, savedLook := goos, goarch, fileOK, lookPath
	t.Cleanup(func() { goos, goarch, fileOK, lookPath = savedOS, savedArch, savedOK, savedLook })
	goos, goarch = os_, arch
	have := map[string]bool{}
	for _, f := range files {
		have[filepath.Clean(f)] = true
	}
	fileOK = func(p string) bool { return have[filepath.Clean(p)] }
	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
}

func TestFindQEMU(t *testing.T) {
	withHost(t, "windows", "amd64", `C:\Program Files\qemu\qemu-system-aarch64.exe`)
	if got, err := findQEMU(""); err != nil || got != `C:\Program Files\qemu\qemu-system-aarch64.exe` {
		t.Errorf("Windows installer's folder: %q %v", got, err)
	}
	withHost(t, "linux", "amd64")
	if _, err := findQEMU(""); err == nil {
		t.Error("found a QEMU that is not there")
	}
	if got, _ := findQEMU("/opt/q"); got != "/opt/q" {
		t.Errorf("--qemu ignored: %q", got)
	}
}

func TestAccelerator(t *testing.T) {
	withHost(t, "windows", "amd64")
	if a := accelerator(); a != "tcg" {
		t.Errorf("x86-64 PC: %s", a)
	}
	withHost(t, "darwin", "arm64")
	if a := accelerator(); a != "hvf" {
		t.Errorf("Apple silicon: %s", a)
	}
	withHost(t, "windows", "arm64")
	if a := accelerator(); a != "tcg" {
		t.Errorf("Windows on ARM: %s", a)
	}
}

// With --print, the command makes the card and says what it would run, and nothing else.
func TestQEMUCommandPrint(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LocalAppData", cache)
	t.Setenv("HOME", cache)
	img := fakeImage(t, t.TempDir(), 0x4d8fd085)
	cardDir := filepath.Join(t.TempDir(), "card")

	code, out, errs := runTool(t, "qemu", "--print", "--qemu", "/opt/qemu-system-aarch64", "--image", img,
		"--card", cardDir, "--game", game(t, "gems", elf.EM_AARCH64), "--", "-monitor", "none")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !exists(cardDir, "games/gems/game") || !strings.Contains(read(t, cardDir, "vedutaos/env"), "VEDUTA_SCALE=4") {
		t.Error("the card was not made")
	}
	for _, want := range []string{"/opt/qemu-system-aarch64 -machine virt", "dir=" + cardDir + ",label=VEDUTA", "-kernel " + filepath.Join(filepath.Dir(img), "vmlinuz"), "root=PARTUUID=4d8fd085-02", "-monitor none"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(cache, "vedutaos", "qemu")); len(entries) != 0 {
		t.Errorf("--print created %d file(s) in the cache", len(entries))
	}
}

func TestQEMUWithoutImage(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LocalAppData", cache)
	t.Setenv("HOME", cache)
	withHost(t, "linux", "amd64")
	if code, _, errs := runTool(t, "qemu", "--print", "--qemu", "/opt/q"); code != 1 || !strings.Contains(errs, "vedutaos image") {
		t.Errorf("exit %d: %s", code, errs)
	}
}
