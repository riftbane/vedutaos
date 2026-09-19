package image

import (
	"bytes"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/riftbane/vedutaos/initramfs"
)

// The Wi-Fi in an initramfs: wpa_supplicant with the shared libraries it loads, the script
// udhcpc runs with an address, and on the Raspberry Pis the radio's firmware. The console's
// own program is static; wpa_supplicant is Debian's, dynamic, so its libraries and the
// dynamic loader come along, found by reading what each file needs (DT_NEEDED) rather than
// by a list that would go stale.

// tree is files and symbolic links to put in an initramfs, by absolute path.
type tree struct {
	files map[string][]byte
	perms map[string]uint32
	links map[string]string
}

func newTree() *tree {
	return &tree{files: map[string][]byte{}, perms: map[string]uint32{}, links: map[string]string{}}
}

func (t *tree) add(p string, perm uint32, b []byte) {
	t.files[p], t.perms[p] = b, perm
}

func (t *tree) merge(o *tree) *tree {
	if o == nil {
		return t
	}
	for p, b := range o.files {
		t.add(p, o.perms[p], b)
	}
	for p, l := range o.links {
		t.links[p] = l
	}
	return t
}

// paths returns every path of the tree, sorted.
func (t *tree) paths() []string {
	var ps []string
	for p := range t.files {
		ps = append(ps, p)
	}
	for p := range t.links {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	return ps
}

// dhcpScript is what udhcpc runs: deconfig when it starts or loses its lease, bound and
// renew with the address in its environment ($ip, $mask, $router, $dns). It sets the
// address, the default route and /etc/resolv.conf with busybox's ip.
const dhcpScript = `#!/bin/busybox sh
ip() { /bin/busybox ip "$@"; }
case "$1" in
deconfig)
	ip -4 addr flush dev "$interface"
	ip link set "$interface" up
	;;
bound|renew)
	if [ "$1" = bound ]; then ip -4 addr flush dev "$interface"; fi
	ip addr replace "$ip/${mask:-24}" dev "$interface"
	if [ -n "$router" ]; then
		ip route del default dev "$interface" 2>/dev/null
		for r in $router; do ip route add default via "$r" dev "$interface"; break; done
	fi
	: > /etc/resolv.conf
	for d in $dns; do echo "nameserver $d" >> /etc/resolv.conf; done
	;;
esac
exit 0
`

// libDirs are where Debian's packages put shared libraries, relative to a package's root.
var libDirs = []string{"usr/lib/aarch64-linux-gnu", "lib/aarch64-linux-gnu", "usr/lib", "lib"}

// supplicantTree gathers wpa_supplicant from the unpacked packages under roots, with every
// library it needs, the libraries those need, and the dynamic loader: the program in
// initramfs.WPASupplicant, the rest in initramfs.Libs, where the loader looks.
func supplicantTree(roots []string) (*tree, error) {
	t := newTree()
	t.add(initramfs.DHCPScript, 0o755, []byte(dhcpScript))
	var prog []byte
	for _, r := range roots {
		for _, p := range []string{"usr/sbin/wpa_supplicant", "sbin/wpa_supplicant"} {
			if b, err := readInRoot(r, p); err == nil {
				prog = b
			}
		}
	}
	if prog == nil {
		return nil, errors.New("wifi: no package holds wpa_supplicant")
	}
	t.add(initramfs.WPASupplicant, 0o755, prog)
	queue := [][]byte{prog}
	seen := map[string]bool{}
	for len(queue) > 0 {
		b := queue[0]
		queue = queue[1:]
		needed, interp, err := dynamicNeeds(b)
		if err != nil {
			return nil, err
		}
		if interp != "" && !seen[interp] {
			seen[interp] = true
			if path.Dir(interp) != initramfs.Libs {
				return nil, fmt.Errorf("wifi: the dynamic loader is %s, not in %s", interp, initramfs.Libs)
			}
			lb, err := findLib(roots, path.Base(interp))
			if err != nil {
				return nil, err
			}
			t.add(interp, 0o755, lb)
		}
		for _, name := range needed {
			if seen[name] {
				continue
			}
			seen[name] = true
			lb, err := findLib(roots, name)
			if err != nil {
				return nil, err
			}
			// Executable like the loader, which libc names among its own needs: the one
			// file is both.
			t.add(path.Join(initramfs.Libs, name), 0o755, lb)
			queue = append(queue, lb)
		}
	}
	return t, nil
}

// dynamicNeeds returns the libraries an arm64 ELF file needs and its dynamic loader.
func dynamicNeeds(b []byte) (needed []string, interp string, err error) {
	f, err := elf.NewFile(bytes.NewReader(b))
	if err != nil {
		return nil, "", fmt.Errorf("wifi: not an ELF file: %w", err)
	}
	defer f.Close()
	if f.Machine != elf.EM_AARCH64 {
		return nil, "", fmt.Errorf("wifi: a file built for %s, not arm64", f.Machine)
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			s, err := io.ReadAll(p.Open())
			if err != nil {
				return nil, "", err
			}
			interp = strings.TrimRight(string(s), "\x00")
		}
	}
	needed, err = f.ImportedLibraries()
	if err != nil && f.Section(".dynamic") != nil {
		return nil, "", err
	}
	return needed, interp, nil
}

// findLib reads a shared library by its name from the first package that has it.
func findLib(roots []string, name string) ([]byte, error) {
	for _, r := range roots {
		for _, d := range libDirs {
			if b, err := readInRoot(r, path.Join(d, name)); err == nil {
				return b, nil
			}
		}
	}
	return nil, fmt.Errorf("wifi: no package holds %s, which wpa_supplicant needs: pin the package that has it", name)
}

// readInRoot reads a file of an unpacked package, following its symbolic links inside the
// package: a link to an absolute path means that path in the package, not on this machine.
func readInRoot(root, rel string) ([]byte, error) {
	real, err := resolveInRoot(root, rel, nil)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(root, filepath.FromSlash(real)))
}

// resolveInRoot follows the links of rel inside root, and returns the path of the file it
// comes to, relative to root. alt replaces a path by another first: the links Debian's
// alternatives make when a package is installed, which an unpacked package lacks.
func resolveInRoot(root, rel string, alt map[string]string) (string, error) {
	rel = path.Clean(strings.TrimPrefix(rel, "/"))
	for hops := 0; hops < 16; hops++ {
		if a, ok := alt[rel]; ok {
			rel = a
		}
		fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			if !fi.Mode().IsRegular() {
				return "", fmt.Errorf("%s is not a file", rel)
			}
			return rel, nil
		}
		target, err := os.Readlink(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		if path.IsAbs(target) {
			rel = path.Clean(strings.TrimPrefix(target, "/"))
		} else {
			rel = path.Clean(path.Join(path.Dir(rel), target))
		}
		if rel == ".." || strings.HasPrefix(rel, "../") {
			return "", fmt.Errorf("%s leads out of the package", target)
		}
	}
	return "", fmt.Errorf("%s: too many links", rel)
}

// firmwareDir is where Debian's firmware packages put the kernel's firmware files.
const firmwareDir = "usr/lib/firmware"

// firmwareAlternatives are the links the Raspberry Pi firmware package makes when it is
// installed (update-alternatives in its postinst), with the choice it prefers.
var firmwareAlternatives = map[string]string{
	firmwareDir + "/cypress/cyfmac43455-sdio.bin": firmwareDir + "/cypress/cyfmac43455-sdio-standard.bin",
}

// piWiFiRequired are firmware files a Raspberry Pi board's radio cannot start without: the
// build fails if a new pin loses them.
var piWiFiRequired = []string{"brcmfmac43455-sdio.raspberrypi,5-model-b.bin", "brcmfmac43455-sdio.raspberrypi,4-model-b.bin", "brcmfmac43436-sdio.raspberrypi,model-zero-2-w.bin"}

// piFirmwareTree gathers the radio firmware of the Raspberry Pi boards: every file in brcm/
// named for one of them (brcmfmac asks for the board's own name first), as a link to the
// file it stands for, which is put in once.
func piFirmwareTree(root string) (*tree, error) {
	t := newTree()
	names, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(firmwareDir), "brcm"))
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, e := range names {
		if !strings.Contains(e.Name(), ".raspberrypi,") {
			continue
		}
		rel := firmwareDir + "/brcm/" + e.Name()
		real, err := resolveInRoot(root, rel, firmwareAlternatives)
		if err != nil {
			return nil, fmt.Errorf("wifi firmware %s: %w", e.Name(), err)
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(real)))
		if err != nil {
			return nil, err
		}
		dst := path.Join(initramfs.Firmware, strings.TrimPrefix(real, firmwareDir+"/"))
		t.add(dst, 0o644, b)
		if name := path.Join(initramfs.Firmware, "brcm", e.Name()); name != dst {
			t.links[name] = dst
		}
		have[e.Name()] = true
	}
	for _, n := range piWiFiRequired {
		if !have[n] {
			return nil, fmt.Errorf("wifi firmware: the package has no %s", n)
		}
	}
	return t, nil
}
