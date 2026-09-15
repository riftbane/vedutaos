package main

import (
	"debug/elf"
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
			Kernel: "/src/vedutaos/out/vmlinuz", Initrd: "/src/vedutaos/out/initrd.img",
			Card: "/home/me/.cache/vedutaos/qemu/card", Accel: "tcg", CPUs: 4, Memory: "1G",
		},
		"qemu-windows.txt": {
			Kernel: `C:\vedutaos\vmlinuz`, Initrd: `C:\vedutaos\initrd.img`, Card: `D:\cards\one,two`,
			Accel: "tcg", CPUs: 4, Memory: "1G", Display: "sdl",
			Extra: []string{"-monitor", "tcp:127.0.0.1:4444,server,nowait"},
		},
		"qemu-kvm.txt": {
			Kernel: "/i/vmlinuz", Initrd: "/i/initrd.img", Card: "/card", Accel: "kvm", CPUs: 2, Memory: "1G",
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
	args := strings.Join(qemuArgs(qemuConfig{Card: "/a,b", CPUs: 1, Memory: "1G"}), " ")
	if !strings.Contains(args, "dir=/a,,b,label=VEDUTA") {
		t.Fatal(args)
	}
}

// kernelFiles writes stand-ins for the kernel and initramfs QEMU boots.
func kernelFiles(t *testing.T, dir string) string {
	t.Helper()
	for _, f := range []string{"vmlinuz", "initrd.img"} {
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	return filepath.Join(dir, "vmlinuz")
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
	kernel := kernelFiles(t, t.TempDir())
	cardDir := filepath.Join(t.TempDir(), "card")

	code, out, errs := runTool(t, "qemu", "--print", "--qemu", "/opt/qemu-system-aarch64", "--kernel", kernel,
		"--card", cardDir, "--game", game(t, "gems", elf.EM_AARCH64), "--debug", "--", "-monitor", "none")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !exists(cardDir, "games/gems/game") || !strings.Contains(read(t, cardDir, "vedutaos/env"), "VEDUTA_SCALE=4") || !exists(cardDir, "vedutaos/debug") {
		t.Error("the card was not made")
	}
	for _, want := range []string{"/opt/qemu-system-aarch64 -machine virt", "dir=" + cardDir + ",label=VEDUTA", "-kernel " + kernel, "-initrd " + filepath.Join(filepath.Dir(kernel), "initrd.img"), "console=ttyAMA0", "-monitor none"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "root=") || strings.Contains(out, "qcow2") || strings.Contains(out, "netdev") {
		t.Errorf("a disk, a root or a network in the command:\n%s", out)
	}
}

func TestQEMUWithoutKernel(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LocalAppData", cache)
	t.Setenv("HOME", cache)
	withHost(t, "linux", "amd64")
	if code, _, errs := runTool(t, "qemu", "--print", "--qemu", "/opt/q"); code != 1 || !strings.Contains(errs, "vedutaos image") {
		t.Errorf("exit %d: %s", code, errs)
	}
}
