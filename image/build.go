// Package image builds the VedutaOS image: Raspberry Pi OS Lite (64-bit) with the console
// installed, which boots on a Raspberry Pi and, with the Debian kernel it also carries, on
// QEMU's virt machine. One image, tested where it is deployed.
//
// Building needs Linux, root, and the tools an Ubuntu or Debian machine has: xz, sfdisk,
// losetup, e2fsck, resize2fs, mount and chroot, plus qemu-user-static registered in binfmt
// so the image's own arm64 programs run in the chroot. It runs on a GitHub Actions runner.
package image

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/riftbane/vedutaos/panel"
)

// Base is the Raspberry Pi OS Lite (64-bit) release the image is built from, pinned so
// that a build is reproducible until the pin moves.
type Base struct {
	URL    string // the .img.xz
	SHA256 string // of the .img.xz, as Raspberry Pi publishes it
}

// Stock is the current base.
var Stock = Base{
	URL:    "https://downloads.raspberrypi.com/raspios_lite_arm64/images/raspios_lite_arm64-2026-09-15/2026-09-15-raspios-trixie-arm64-lite.img.xz",
	SHA256: "cdf4f3bfac35ae947b46e4e767f935453810549779ac3290e05a6754aee627e5",
}

// Overlay is copied into the image's root as it is: units, the console's scripts, defaults.
//
//go:embed rootfs
var Overlay embed.FS

//go:embed chroot.sh
var chrootScript []byte

// Options say what to build.
type Options struct {
	Base    Base
	VShell  string // the dashboard, built for linux/arm64
	Version string // written to /etc/vedutaos-release
	Out     string // directory for vedutaos.img, vmlinuz and initrd.img
	Cache   string // directory keeping the downloaded base; Out when empty
	Grow    int64  // bytes added to the root partition; 1 GiB when zero
	Panel   panel.Options
	Wiring  Wiring // DefaultPins and DefaultSPISpeed when zero
	Verbose io.Writer
}

// Result names what Build wrote.
type Result struct {
	Image, Kernel, Initrd string
}

// Paths inside the image, for the tests and the tool.
const (
	ImageName  = "vedutaos.img"
	KernelName = "vmlinuz"
	InitrdName = "initrd.img"
	Release    = "/etc/vedutaos-release"
	rootMount  = "/boot/firmware"
)

// Check reports why this machine cannot build an image, or nil.
func Check() error {
	if runtime.GOOS != "linux" {
		return errors.New("vedutaos image: builds on Linux only (a GitHub Actions runner does)")
	}
	if os.Geteuid() != 0 {
		return errors.New("vedutaos image: needs root, for losetup, mount and chroot")
	}
	for _, t := range []string{"xz", "sfdisk", "losetup", "e2fsck", "resize2fs", "mount", "umount", "chroot"} {
		if _, err := exec.LookPath(t); err != nil {
			return fmt.Errorf("vedutaos image: %s is not installed", t)
		}
	}
	if _, err := os.Stat("/proc/sys/fs/binfmt_misc/qemu-aarch64"); err != nil {
		return errors.New("vedutaos image: arm64 programs cannot run here: install qemu-user-static (binfmt-support)")
	}
	return nil
}

// Build makes the image.
func Build(o Options) (Result, error) {
	var r Result
	if err := Check(); err != nil {
		return r, err
	}
	if o.Base.URL == "" {
		o.Base = Stock
	}
	if o.Cache == "" {
		o.Cache = o.Out
	}
	if o.Grow == 0 {
		o.Grow = 1 << 30
	}
	if o.Wiring.Speed == 0 {
		o.Wiring.Speed = DefaultSPISpeed
	}
	if o.Wiring.Pins == (Pins{}) {
		o.Wiring.Pins = DefaultPins
	}
	if o.Verbose == nil {
		o.Verbose = io.Discard
	}
	if o.VShell == "" || o.Version == "" || o.Out == "" {
		return r, errors.New("image: VShell, Version and Out are required")
	}
	if err := os.MkdirAll(o.Out, 0o755); err != nil {
		return r, err
	}
	if err := os.MkdirAll(o.Cache, 0o755); err != nil {
		return r, err
	}

	xzPath := filepath.Join(o.Cache, filepath.Base(o.Base.URL))
	if err := fetch(o.Base, xzPath, o.Verbose); err != nil {
		return r, err
	}
	r.Image = filepath.Join(o.Out, ImageName)
	r.Kernel = filepath.Join(o.Out, KernelName)
	r.Initrd = filepath.Join(o.Out, InitrdName)

	fmt.Fprintf(o.Verbose, "unpacking %s\n", xzPath)
	if err := unxz(xzPath, r.Image); err != nil {
		return r, err
	}
	if err := grow(r.Image, o.Grow); err != nil {
		return r, err
	}

	m, err := attach(r.Image)
	if err != nil {
		return r, err
	}
	defer m.detach()
	fmt.Fprintf(o.Verbose, "image on %s, root at %s\n", m.loop, m.root)

	if err := install(m.root, o); err != nil {
		return r, err
	}
	// The kernel QEMU boots is the Debian one, the only -arm64 flavour in the image.
	kernels, _ := filepath.Glob(filepath.Join(m.root, "boot", "vmlinuz-*-arm64"))
	if len(kernels) != 1 {
		return r, fmt.Errorf("image: want one Debian kernel in /boot, found %d", len(kernels))
	}
	kver := strings.TrimPrefix(filepath.Base(kernels[0]), "vmlinuz-")
	if err := copyOut(kernels[0], r.Kernel, 0o644); err != nil {
		return r, err
	}
	if err := copyOut(filepath.Join(m.root, "boot", "initrd.img-"+kver), r.Initrd, 0o644); err != nil {
		return r, err
	}
	if err := m.detach(); err != nil {
		return r, err
	}
	return r, nil
}

// fetch downloads b to dst unless dst already matches; nothing is left unless it does.
func fetch(b Base, dst string, out io.Writer) error {
	if sum, err := fileSum(dst); err == nil && sum == b.SHA256 {
		fmt.Fprintf(out, "base image cached: %s\n", dst)
		return nil
	}
	fmt.Fprintf(out, "downloading %s\n", b.URL)
	resp, err := http.Get(b.URL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", b.URL, resp.Status)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("download: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != b.SHA256 {
		os.Remove(tmp)
		return fmt.Errorf("download %s: sha256 %s, want %s", filepath.Base(dst), got, b.SHA256)
	}
	return os.Rename(tmp, dst)
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func unxz(src, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.Command("xz", "-dc", "-T0", src)
	cmd.Stdout = f
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// grow adds n bytes to the image and to its second, root, partition (the file system is
// grown once the image is attached).
func grow(img string, n int64) error {
	st, err := os.Stat(img)
	if err != nil {
		return err
	}
	if err := os.Truncate(img, st.Size()+n); err != nil {
		return err
	}
	return run("", strings.NewReader(", +\n"), "sfdisk", "-q", "-N", "2", img)
}

// mounted is an attached image: its loop device and the mounted root.
type mounted struct {
	loop, root string
	undo       []func() error
}

func attach(img string) (*mounted, error) {
	out, err := exec.Command("losetup", "-fP", "--show", img).Output()
	if err != nil {
		return nil, fmt.Errorf("losetup: %w", err)
	}
	m := &mounted{loop: strings.TrimSpace(string(out))}
	m.undo = append(m.undo, func() error { return run("", nil, "losetup", "-d", m.loop) })
	fail := func(err error) (*mounted, error) { m.detach(); return nil, err }
	root, boot := m.loop+"p2", m.loop+"p1"
	// e2fsck -p exits 1 when it fixed something, which is fine after a resize.
	if err := run("", nil, "e2fsck", "-fp", root); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() > 1 {
			return fail(err)
		}
	}
	if err := run("", nil, "resize2fs", root); err != nil {
		return fail(err)
	}
	dir, err := os.MkdirTemp("", "vedutaos-root-")
	if err != nil {
		return fail(err)
	}
	m.root = dir
	m.undo = append(m.undo, func() error { return os.Remove(dir) })
	for _, mnt := range []struct {
		src, dst string
		opts     []string
	}{
		{root, dir, nil},
		{boot, filepath.Join(dir, rootMount), nil},
		{"proc", filepath.Join(dir, "proc"), []string{"-t", "proc"}},
		{"/sys", filepath.Join(dir, "sys"), []string{"--rbind", "--make-rslave"}},
		{"/dev", filepath.Join(dir, "dev"), []string{"--rbind", "--make-rslave"}},
	} {
		args := append(append([]string{}, mnt.opts...), mnt.src, mnt.dst)
		if err := run("", nil, "mount", args...); err != nil {
			return fail(err)
		}
		dst := mnt.dst
		m.undo = append(m.undo, func() error { return run("", nil, "umount", "-R", dst) })
	}
	return m, nil
}

// detach undoes what attach did, in reverse; it is safe to call twice.
func (m *mounted) detach() error {
	var first error
	for i := len(m.undo) - 1; i >= 0; i-- {
		if err := m.undo[i](); err != nil && first == nil {
			first = err
		}
	}
	m.undo = nil
	return first
}

// install puts the console into the mounted root.
func install(root string, o Options) error {
	// The chroot resolves names through the host's resolver while it installs.
	resolv := filepath.Join(root, "etc", "resolv.conf")
	saved, _ := os.ReadFile(resolv)
	host, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return err
	}
	os.Remove(resolv)
	if err := os.WriteFile(resolv, host, 0o644); err != nil {
		return err
	}
	defer func() {
		os.Remove(resolv)
		if saved != nil {
			os.WriteFile(resolv, saved, 0o644)
		}
	}()

	if err := copyOverlay(root); err != nil {
		return err
	}
	if err := copyOut(o.VShell, filepath.Join(root, "usr", "lib", "vedutaos", "vshell"), 0o755); err != nil {
		return err
	}
	cmds, err := panel.ILI9341(o.Panel)
	if err != nil {
		return err
	}
	fw, err := panel.Encode(cmds)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "lib", "firmware", panel.FirmwareName), fw, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, Release), []byte(o.Version+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "hostname"), []byte("vedutaos\n"), 0o644); err != nil {
		return err
	}
	if err := edit(filepath.Join(root, "etc", "hosts"), func(b []byte) ([]byte, error) {
		return bytes.ReplaceAll(b, []byte("raspberrypi"), []byte("vedutaos")), nil
	}); err != nil {
		return err
	}
	fwDir := filepath.Join(root, rootMount)
	if err := edit(filepath.Join(fwDir, "config.txt"), func(b []byte) ([]byte, error) {
		return ConfigTxt(b, &o.Wiring), nil
	}); err != nil {
		return err
	}
	if err := edit(filepath.Join(fwDir, "cmdline.txt"), Cmdline); err != nil {
		return err
	}

	before, err := firmwareSums(fwDir)
	if err != nil {
		return err
	}
	script := filepath.Join(root, "tmp", "vedutaos-chroot.sh")
	if err := os.WriteFile(script, chrootScript, 0o755); err != nil {
		return err
	}
	defer os.Remove(script)
	if err := run(root, nil, "chroot", root, "/tmp/vedutaos-chroot.sh"); err != nil {
		return fmt.Errorf("chroot: %w", err)
	}
	after, err := firmwareSums(fwDir)
	if err != nil {
		return err
	}
	if before != after {
		return errors.New("image: the chroot changed the Pi kernels in " + rootMount)
	}
	return nil
}

// firmwareSums fingerprints the Pi kernels and initramfs on the boot partition.
func firmwareSums(dir string) (string, error) {
	var b strings.Builder
	for _, g := range []string{"kernel*.img", "initramfs*"} {
		names, _ := filepath.Glob(filepath.Join(dir, g))
		for _, n := range names {
			s, err := fileSum(n)
			if err != nil {
				return "", err
			}
			b.WriteString(filepath.Base(n) + " " + s + "\n")
		}
	}
	return b.String(), nil
}

func copyOverlay(root string) error {
	return fs.WalkDir(Overlay, "rootfs", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, "rootfs")
		if rel == "" {
			return nil
		}
		dst := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := Overlay.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(rel, "/usr/lib/vedutaos/") {
			mode = 0o755
		}
		os.Remove(dst)
		return os.WriteFile(dst, data, mode)
	})
}

func copyOut(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}

func edit(path string, f func([]byte) ([]byte, error)) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	b, err = f(b)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func run(dir string, stdin io.Reader, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = stdin
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
