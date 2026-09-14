package main

import (
	"crypto/sha512"
	"debug/elf"
	"encoding/hex"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update-golden", false, "rewrite the reference command lines")

// The QEMU command line is pinned: each part of it was found necessary on a real run (see
// docs/quickstart-qemu.md), and a change should be tried on one before it is accepted.
func TestQEMUArgsGolden(t *testing.T) {
	cases := map[string]qemuConfig{
		"qemu-linux.txt": {
			Firmware: "/usr/share/qemu-efi-aarch64/QEMU_EFI.fd", Disk: "/home/me/.cache/vedutaos/qemu/console.qcow2",
			Card: "/home/me/.cache/vedutaos/qemu/card", Accel: "tcg", CPUs: 4, Memory: "2G", SSHPort: 2222,
		},
		"qemu-windows.txt": {
			Firmware: `C:\Program Files\qemu\share\edk2-aarch64-code.fd`, Disk: `C:\Users\Me\AppData\Local\vedutaos\qemu\console.qcow2`,
			Card: `D:\cards\one,two`, Accel: "tcg", CPUs: 4, Memory: "2G", SSHPort: 2223, Display: "sdl",
			Extra: []string{"-monitor", "tcp:127.0.0.1:4444,server,nowait"},
		},
		"qemu-kvm.txt": {
			Firmware: "/usr/share/AAVMF/AAVMF_CODE.fd", Disk: "/d.qcow2", Card: "/card", Accel: "kvm", CPUs: 2, Memory: "1G", SSHPort: 2222,
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
	if !strings.Contains(args, "dir=/a,,b,label=CIDATA") || !strings.Contains(args, "file.filename=/c,,d ") {
		t.Fatal(args)
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

func TestFindFirmware(t *testing.T) {
	withHost(t, "linux", "amd64", "/usr/share/qemu-efi-aarch64/QEMU_EFI.fd", "/usr/share/AAVMF/AAVMF_CODE.fd")
	if got, err := findFirmware("/usr/bin/qemu-system-aarch64"); err != nil || got != "/usr/share/qemu-efi-aarch64/QEMU_EFI.fd" {
		t.Errorf("Ubuntu: %q %v", got, err)
	}
	win := filepath.Join("qemu", "share", "edk2-aarch64-code.fd")
	withHost(t, "windows", "amd64", win)
	if got, err := findFirmware(filepath.Join("qemu", "qemu-system-aarch64.exe")); err != nil || got != win {
		t.Errorf("beside QEMU: %q %v", got, err)
	}
	withHost(t, "windows", "amd64")
	if got, err := findFirmware(`D:\tools\qemu\qemu-system-aarch64.exe`); err != nil || got != "edk2-aarch64-code.fd" {
		t.Errorf("Windows, laid out some other way: %q %v (QEMU finds a bare name in its own data folder)", got, err)
	}
	withHost(t, "linux", "amd64")
	if _, err := findFirmware("/usr/bin/qemu-system-aarch64"); err == nil || !strings.Contains(err.Error(), "qemu-efi-aarch64") {
		t.Errorf("no firmware: %v", err)
	}
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

func imageServer(t *testing.T, image []byte, sums string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/SHA512SUMS":
			w.Write([]byte(sums))
		case "/" + imageName:
			w.Header().Set("Content-Length", strconv.Itoa(len(image)))
			w.Write(image)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchImage(t *testing.T) {
	image := []byte(strings.Repeat("qcow2", 1000))
	sum := sha512.Sum512(image)
	good := "0000  debian-13-generic-arm64.json\n" + hex.EncodeToString(sum[:]) + "  " + imageName + "\n"

	dst := filepath.Join(t.TempDir(), imageName)
	var out strings.Builder
	if err := fetchImage(imageServer(t, image, good).URL+"/", imageName, dst, &out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != string(image) {
		t.Error("the image arrived changed")
	}
	if !strings.Contains(out.String(), "100%") {
		t.Errorf("no progress: %q", out.String())
	}

	bad := strings.Replace(good, hex.EncodeToString(sum[:])[:8], "deadbeef", 1)
	for name, srv := range map[string]*httptest.Server{
		"mismatch":  imageServer(t, image, bad),
		"not named": imageServer(t, image, "abc  other.qcow2\n"),
	} {
		dst := filepath.Join(t.TempDir(), imageName)
		if err := fetchImage(srv.URL+"/", imageName, dst, &out); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if entries, _ := os.ReadDir(filepath.Dir(dst)); len(entries) != 0 {
			t.Errorf("%s: left %d file(s) behind", name, len(entries))
		}
	}
	srv := imageServer(t, image, good)
	if err := fetchImage(srv.URL+"/missing/", imageName, filepath.Join(t.TempDir(), "x"), &out); err == nil {
		t.Error("a 404 was accepted")
	}
}

// With --print, the command makes the card and says what it would run, and nothing else.
func TestQEMUCommandPrint(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LocalAppData", cache)
	t.Setenv("HOME", cache)
	vshell := fakeProgram(t, filepath.Join(t.TempDir(), "vshell"), elf.EM_AARCH64)
	fw := filepath.Join(t.TempDir(), "QEMU_EFI.fd")
	os.WriteFile(fw, []byte("fd"), 0o644)
	cardDir := filepath.Join(t.TempDir(), "card")

	code, out, errs := runTool(t, "qemu", "--print", "--qemu", "/opt/qemu-system-aarch64", "--firmware", fw,
		"--vshell", vshell, "--card", cardDir, "--game", game(t, "gems", elf.EM_AARCH64), "--", "-monitor", "none")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !exists(cardDir, "games/gems/game") || !strings.Contains(read(t, cardDir, "vedutaos/env"), "VEDUTA_SCALE=4") {
		t.Error("the card was not made")
	}
	for _, want := range []string{"/opt/qemu-system-aarch64 -machine virt", "dir=" + cardDir + ",label=CIDATA", "-bios " + fw, "-monitor none"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(cache, "vedutaos", "qemu", "*.qcow2"))
	if len(matches) != 0 {
		t.Errorf("--print made %v", matches)
	}
}
