package image

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/riftbane/vedutaos/initramfs"
)

// TestSupplicantTreeFromStock gathers wpa_supplicant from the pinned packages when a build
// has unpacked them (out/cache): every library it needs is there, and the loader, which
// libc also names as a library, stays executable.
func TestSupplicantTreeFromStock(t *testing.T) {
	var roots []string
	for _, p := range Stock.WiFi {
		root := filepath.Join("..", "out", "cache", "pkg", p.Name)
		if _, err := os.Stat(root); err != nil {
			t.Skip("no unpacked packages: build an image first (vedutaos image --out out --cache out/cache)")
		}
		roots = append(roots, root)
	}
	tr, err := supplicantTree(roots)
	if err != nil {
		t.Fatal(err)
	}
	loader := initramfs.Libs + "/ld-linux-aarch64.so.1"
	for _, p := range []string{initramfs.WPASupplicant, loader, initramfs.Libs + "/libc.so.6", initramfs.Libs + "/libssl.so.3", initramfs.Libs + "/libnl-genl-3.so.200"} {
		if _, ok := tr.files[p]; !ok {
			t.Errorf("%s missing", p)
		} else if tr.perms[p]&0o111 == 0 {
			t.Errorf("%s is not executable (%o)", p, tr.perms[p])
		}
	}
	// Run it where the machine can (an arm64 host, or qemu-user), from a copy of the tree.
	qemu, err := exec.LookPath("qemu-aarch64-static")
	if err != nil {
		return
	}
	dir := t.TempDir()
	for p, b := range tr.files {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), b, os.FileMode(tr.perms[p]))
	}
	out, err := exec.Command(qemu, "-L", dir, filepath.Join(dir, initramfs.WPASupplicant), "-v").CombinedOutput()
	if err != nil {
		t.Fatalf("wpa_supplicant -v: %v\n%s", err, out)
	}
}
