package image

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"debug/elf"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/riftbane/vedutaos/card"
	"github.com/riftbane/vedutaos/initramfs"
	"github.com/riftbane/vedutaos/panel"
)

// TestBuild builds an image from fake packages served from memory: the layout of the card
// and of the initramfs is what the boot firmware, the kernels and init expect.
func TestBuild(t *testing.T) {
	if err := Check(); err != nil {
		t.Skip(err)
	}
	served := map[string][]byte{}
	pin := func(name string, files map[string][]byte) Package {
		deb := fakeDeb(files)
		url := "http://pins/" + name + ".deb"
		served[url] = deb
		return Package{Name: name, Version: "1", URLs: []string{url}, SHA256: sumOf(deb)}
	}
	pi := map[string][]string{"panel_mipi_dbi": {"drm"}, "drm": nil, "spi_bcm2835": nil, "spi_dw_mmio": {"spi_dw"}, "spi_dw": nil, "gpio_backlight": nil, "pwm_bl": nil, "gpio_keys": nil,
		"brcmfmac": {"cfg80211"}, "cfg80211": nil, "brcmfmac_wcc": {"brcmfmac"}, "brcmfmac_cyw": {"brcmfmac"}, "brcmfmac_bca": {"brcmfmac"}}
	// The Raspberry Pi firmware package, laid out as it is: board names linking to the
	// generic files, and the Pi 4 and 5's to a link its postinst makes.
	fw := "usr/lib/firmware/"
	wifiFirmware := map[string][]byte{
		fw + "cypress/cyfmac43455-sdio-standard.bin":                  []byte("43455 standard"),
		fw + "cypress/cyfmac43455-sdio-minimal.bin":                   []byte("43455 minimal"),
		fw + "brcm/brcmfmac43455-sdio.raspberrypi,5-model-b.bin":      []byte("-> ../cypress/cyfmac43455-sdio.bin"),
		fw + "brcm/brcmfmac43455-sdio.raspberrypi,4-model-b.bin":      []byte("-> ../cypress/cyfmac43455-sdio.bin"),
		fw + "brcm/brcmfmac43455-sdio.raspberrypi,5-model-b.txt":      []byte("nvram"),
		fw + "brcm/brcmfmac43436-sdio.bin":                            []byte("43436"),
		fw + "brcm/brcmfmac43436-sdio.raspberrypi,model-zero-2-w.bin": []byte("-> brcmfmac43436-sdio.bin"),
		fw + "brcm/brcmfmac4356-pcie.bin":                             []byte("not a Pi's"),
	}
	piBuiltin := []string{"vfat", "nls_cp437", "nls_ascii", "xhci_pci", "usbhid", "hid_generic", "evdev"}
	pkgs := Packages{
		Firmware:   pin("raspi-firmware", map[string][]byte{"usr/lib/raspi-firmware/start.elf": []byte("start"), "usr/lib/raspi-firmware/bootcode.bin": []byte("boot"), "usr/lib/raspi-firmware/fixup.dat": []byte("fix")}),
		KernelV8:   pin("linux-v8", fakeKernel(t, "6.18.0-v8", pi, piBuiltin)),
		Kernel2712: pin("linux-2712", fakeKernel(t, "6.18.0-2712", pi, piBuiltin)),
		KernelSunxi: pin("linux-sunxi", fakeSunxiKernel(t, "6.18.0-sunxi64", map[string][]string{
			"panel_mipi_dbi": {"drm_mipi_dbi"}, "drm_mipi_dbi": nil, "gpio_backlight": nil, "gpio_keys": nil, "sunxi": {"musb_hdrc"}, "musb_hdrc": nil,
		}, []string{"vfat", "nls_cp437", "usbhid", "hid_generic", "evdev"})),
		UBootZero2W: pin("u-boot-zero2w", map[string][]byte{"usr/lib/linux-u-boot-current-orangepizero2w/u-boot-sunxi-with-spl.bin": []byte("eGON.BT0 u-boot")}),
		KernelVirt: pin("linux-virt", fakeKernel(t, "6.12.0-arm64", map[string][]string{
			"virtio_blk": nil, "virtio_gpu": {"drm"}, "drm": nil, "xhci_pci": {"xhci_hcd"}, "xhci_hcd": nil,
			"mac80211_hwsim": {"mac80211"}, "mac80211": {"cfg80211"}, "cfg80211": nil, "ccm": nil, "cmac": nil,
			"usbhid": {"hid"}, "hid_generic": {"hid"}, "hid": nil, "evdev": nil, "vfat": {"fat"}, "fat": nil, "nls_cp437": nil, "nls_ascii": nil,
		}, nil)),
		Busybox:      pin("busybox-static", map[string][]byte{"usr/bin/busybox": []byte("busybox!")}),
		WiFiFirmware: pin("firmware-brcm80211", wifiFirmware),
		Certificates: pin("ca-certificates", map[string][]byte{
			"usr/share/ca-certificates/mozilla/B_Root.crt": []byte("-----B-----"),
			"usr/share/ca-certificates/mozilla/A_Root.crt": []byte("-----A-----\n"),
		}),
		WiFi: []Package{pin("wpasupplicant", map[string][]byte{"usr/sbin/wpa_supplicant": minimalELF(elf.ET_EXEC, elf.EM_AARCH64)})},
	}
	get := func(url string) (io.ReadCloser, error) {
		b, ok := served[url]
		if !ok {
			return nil, os.ErrNotExist
		}
		return io.NopCloser(bytes.NewReader(b)), nil
	}
	vshell := minimalELF(elf.ET_EXEC, elf.EM_AARCH64)
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "vshell"), vshell, 0o755)
	// A game folder goes onto the card as it is.
	game := filepath.Join(work, "cube")
	os.MkdirAll(filepath.Join(game, "assets"), 0o755)
	os.WriteFile(filepath.Join(game, "card.json"), []byte(`{"veduta": "card/1", "title": "cube", "name": "cube", "exec": "cube"}`), 0o644)
	os.WriteFile(filepath.Join(game, "cube"), minimalELF(elf.ET_EXEC, elf.EM_AARCH64), 0o755)
	os.WriteFile(filepath.Join(game, "assets", "x.json"), []byte("{}"), 0o644)
	r, err := Build(Options{
		Packages: pkgs, VShell: filepath.Join(work, "vshell"), Version: "v9.9.9", Out: filepath.Join(work, "out"),
		Size: 64 << 20, Panel: panel.Options{Rotate: 90}, Fetch: get, Games: []string{game},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The card.
	fi, err := os.Stat(r.Image)
	if err != nil || fi.Size() != 64<<20 {
		t.Fatalf("image: %v %v", fi, err)
	}
	f, _ := os.Open(r.Image)
	label, err := card.ReadLabel(io.NewSectionReader(f, partitionLBA*sectorSize, 64<<20))
	f.Close()
	if err != nil || label != card.BootLabel {
		t.Errorf("label %q %v", label, err)
	}
	paths, err := listFAT(r.Image)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, p := range paths {
		have[strings.ToLower(p)] = true
	}
	for _, want := range []string{"/kernel8.img", "/initramfs8", "/kernel_2712.img", "/initramfs_2712", "/config.txt", "/cmdline.txt", "/start.elf", "/bootcode.bin", "/bcm2710-x.dtb", "/overlays/mipi-dbi-spi.dtbo", "/overlays/vedutaos-buttons.dtbo", "/vedutaos/release",
		"/extlinux/extlinux.conf", "/sunxi/image", "/sunxi/initrd.img", "/sunxi/" + Zero2WDTB,
		"/games/cube/card.json", "/games/cube/cube", "/games/cube/assets/x.json"} {
		if !have[want] {
			t.Errorf("the card lacks %s; it has %v", want, paths)
		}
	}
	if !have["/games/"] && !have["/games"] {
		t.Errorf("the card lacks the games folder: %v", paths)
	}
	// U-Boot sits where the Allwinner boot ROM reads it, before the partition.
	img, _ := os.ReadFile(r.Image)
	if !bytes.Equal(img[ubootOffset:ubootOffset+15], []byte("eGON.BT0 u-boot")) || img[510] != 0x55 {
		t.Error("U-Boot is not at 8 KiB, or it overwrote the partition table")
	}
	for _, p := range paths {
		if strings.HasSuffix(strings.ToLower(p), "vmlinuz") || strings.HasPrefix(p, "/initrd.img") {
			t.Errorf("the QEMU kernel does not belong on the card: %s", p)
		}
	}

	// The boot files for updates: the card's files without its games.
	boot := map[string]bool{}
	bf, err := os.Open(r.Boot)
	if err != nil {
		t.Fatal(err)
	}
	bgz, err := gzip.NewReader(bf)
	if err != nil {
		t.Fatal(err)
	}
	btr := tar.NewReader(bgz)
	for {
		h, err := btr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		boot[h.Name] = true
	}
	bf.Close()
	for _, want := range []string{"kernel8.img", "initramfs_2712", "config.txt", "overlays/vedutaos-buttons.dtbo", "sunxi/initrd.img", "vedutaos/release", "start.elf"} {
		if !boot[want] {
			t.Errorf("%s lacks %s", BootName, want)
		}
	}
	for name := range boot {
		if strings.HasPrefix(name, "games/") {
			t.Errorf("%s holds the game file %s", BootName, name)
		}
	}

	// The QEMU kernel and its initramfs, beside the image.
	if b, err := os.ReadFile(r.Kernel); err != nil || string(b) != "kernel 6.12.0-arm64" {
		t.Errorf("vmlinuz: %q %v", b, err)
	}
	entries := readInitramfs(t, r.Initrd)
	files := map[string]cpioEntry{}
	// The kernel makes each entry as it comes and drops one whose directory is not there
	// yet, so every directory must precede what is in it, and be there once.
	for _, e := range entries {
		if _, dup := files[e.Name]; dup {
			t.Errorf("%s is in the initramfs twice", e.Name)
		}
		if parent := filepath.Dir(e.Name); parent != "." {
			if d, ok := files[parent]; !ok || d.Mode&modeDir == 0 {
				t.Errorf("%s comes before its directory", e.Name)
			}
		}
		files[e.Name] = e
	}
	if e, ok := files["init"]; !ok || !bytes.Equal(e.Data, vshell) || e.Mode != modeFile|0o755 {
		t.Error("init is not the dashboard, executable")
	}
	if e, ok := files[strings.TrimPrefix(initramfs.Modprobe, "/")]; !ok || e.Mode&0o170000 != modeLink || string(e.Data) != initramfs.Init {
		t.Error("modprobe is not a link to init")
	}
	if e, ok := files[strings.TrimPrefix(initramfs.Busybox, "/")]; !ok || string(e.Data) != "busybox!" {
		t.Error("busybox missing")
	}
	if e, ok := files[strings.TrimPrefix(initramfs.Release, "/")]; !ok || string(e.Data) != "v9.9.9\n" {
		t.Error("release missing")
	}
	if e, ok := files["lib/firmware/"+panel.FirmwareName]; !ok || len(e.Data) < 20 {
		t.Error("panel firmware missing")
	}
	order, ok := files[strings.TrimPrefix(initramfs.ModuleList, "/")]
	if !ok {
		t.Fatal("module list missing")
	}
	lines := strings.Split(strings.TrimSpace(string(order.Data)), "\n")
	var names []string
	for _, l := range lines {
		if !strings.HasPrefix(l, "/lib/modules/6.12.0-arm64/kernel/") || !strings.HasSuffix(l, ".ko") {
			t.Errorf("module list line %q", l)
		}
		e, ok := files[strings.TrimPrefix(l, "/")]
		if !ok {
			t.Errorf("%s listed but not in the initramfs", l)
			continue
		}
		if _, err := elf.NewFile(bytes.NewReader(e.Data)); err != nil {
			t.Errorf("%s is not a plain module: %v", l, err)
		}
		names = append(names, moduleName(l))
	}
	sorted := append([]string{}, names...)
	sort.Strings(sorted)
	want := []string{"ccm", "cfg80211", "cmac", "drm", "evdev", "fat", "hid", "hid_generic", "mac80211", "mac80211_hwsim", "nls_ascii", "nls_cp437", "usbhid", "vfat", "virtio_blk", "virtio_gpu", "xhci_hcd", "xhci_pci"}
	if strings.Join(sorted, " ") != strings.Join(want, " ") {
		t.Errorf("virt modules %v, want %v", sorted, want)
	}
	if indexOf(names, "hid") > indexOf(names, "usbhid") || indexOf(names, "fat") > indexOf(names, "vfat") {
		t.Errorf("dependencies load late: %v", names)
	}
	// QEMU's has wpa_supplicant for its simulated radios, and no Raspberry Pi firmware.
	if e := files[strings.TrimPrefix(initramfs.Certificates, "/")]; string(e.Data) != "-----A-----\n-----B-----\n" {
		t.Errorf("the certificates: %q", e.Data)
	}
	for _, f := range []string{initramfs.WPASupplicant, initramfs.DHCPScript} {
		if e, ok := files[strings.TrimPrefix(f, "/")]; !ok || e.Mode != modeFile|0o755 {
			t.Errorf("%s missing, or not executable", f)
		}
	}
	if _, ok := files["lib/firmware/brcm"]; ok {
		t.Error("QEMU's initramfs has the Raspberry Pis' Wi-Fi firmware")
	}
	for _, d := range []string{"dev", "proc", "sys", "boot/firmware/games", "tmp"} {
		if e, ok := files[d]; !ok || e.Mode&modeDir == 0 {
			t.Errorf("directory %s missing", d)
		}
	}

	// The Pi initramfs carries the panel and nothing QEMU needs; what is built in is left out.
	mcopy := exec.Command("mcopy", "-i", r.Image+"@@"+"1048576", "-n", "::/initramfs8", filepath.Join(work, "initramfs8"))
	mcopy.Env = append(os.Environ(), "MTOOLS_SKIP_CHECK=1")
	if out, err := mcopy.CombinedOutput(); err != nil {
		t.Fatalf("mcopy: %v\n%s", err, out)
	}
	piFiles := map[string]bool{}
	piEntries := map[string]cpioEntry{}
	for _, e := range readInitramfs(t, filepath.Join(work, "initramfs8")) {
		piEntries[e.Name] = e
		if strings.HasSuffix(e.Name, ".ko") {
			if !strings.HasPrefix(e.Name, "lib/modules/6.18.0-v8/kernel/") {
				t.Errorf("initramfs8 holds %s", e.Name)
			}
			piFiles[moduleName(e.Name)] = true
		} else {
			piFiles[e.Name] = true
		}
	}
	// The radio's firmware: the board names as links to the files, each file put in once,
	// the alternative a package install would pick followed.
	for name, want := range map[string]string{
		"lib/firmware/brcm/brcmfmac43455-sdio.raspberrypi,5-model-b.bin":      "/lib/firmware/cypress/cyfmac43455-sdio-standard.bin",
		"lib/firmware/brcm/brcmfmac43455-sdio.raspberrypi,4-model-b.bin":      "/lib/firmware/cypress/cyfmac43455-sdio-standard.bin",
		"lib/firmware/brcm/brcmfmac43436-sdio.raspberrypi,model-zero-2-w.bin": "/lib/firmware/brcm/brcmfmac43436-sdio.bin",
	} {
		if e, ok := piEntries[name]; !ok || e.Mode&0o170000 != modeLink || string(e.Data) != want {
			t.Errorf("%s: %+v, want a link to %s", name, e, want)
		}
		if e, ok := piEntries[strings.TrimPrefix(want, "/")]; !ok || e.Mode&0o170000 != modeFile {
			t.Errorf("%s missing", want)
		}
	}
	if e := piEntries["lib/firmware/brcm/brcmfmac43455-sdio.raspberrypi,5-model-b.txt"]; string(e.Data) != "nvram" {
		t.Error("the Pi 5's nvram file is missing")
	}
	for _, none := range []string{"lib/firmware/brcm/brcmfmac4356-pcie.bin", "lib/firmware/cypress/cyfmac43455-sdio-minimal.bin"} {
		if _, ok := piEntries[none]; ok {
			t.Errorf("initramfs8 holds %s, no Pi's", none)
		}
	}
	for _, want := range []string{"panel_mipi_dbi", "drm", "spi_bcm2835", "spi_dw_mmio", "spi_dw", "gpio_keys", "brcmfmac", "brcmfmac_wcc", "cfg80211", "init", "bin/wpa_supplicant", "etc/udhcpc.script", "etc/ssl/certs/ca-certificates.crt"} {
		if !piFiles[want] {
			t.Errorf("initramfs8 lacks %s", want)
		}
	}
	for _, none := range []string{"virtio_blk", "virtio_gpu", "evdev", "vfat"} {
		if piFiles[none] {
			t.Errorf("initramfs8 holds %s", none)
		}
	}

	// The Orange Pi's initramfs carries its kernel's panel, backlight, buttons and OTG port,
	// and its tree has the panel in it.
	for _, f := range []string{"sunxi/initrd.img", "sunxi/" + Zero2WDTB} {
		mcopy := exec.Command("mcopy", "-i", r.Image+"@@"+"1048576", "-n", "::/"+f, filepath.Join(work, filepath.Base(f)))
		mcopy.Env = append(os.Environ(), "MTOOLS_SKIP_CHECK=1")
		if out, err := mcopy.CombinedOutput(); err != nil {
			t.Fatalf("mcopy: %v\n%s", err, out)
		}
	}
	var sunxiMods []string
	for _, e := range readInitramfs(t, filepath.Join(work, "initrd.img")) {
		if strings.HasSuffix(e.Name, ".ko") {
			sunxiMods = append(sunxiMods, moduleName(e.Name))
		}
		if e.Name == "init" && !bytes.Equal(e.Data, vshell) {
			t.Error("the Orange Pi's init is not the dashboard")
		}
	}
	sort.Strings(sunxiMods)
	if got, want := strings.Join(sunxiMods, " "), "drm_mipi_dbi gpio_backlight gpio_keys musb_hdrc panel_mipi_dbi sunxi"; got != want {
		t.Errorf("Orange Pi modules %s, want %s", got, want)
	}
	if dtb, _ := os.ReadFile(filepath.Join(work, Zero2WDTB)); !bytes.Contains(dtb, []byte(panel.Compatible)) {
		t.Error("the Orange Pi's device tree has no panel")
	}
}

func TestBuildRefusesAForeignDashboard(t *testing.T) {
	if err := Check(); err != nil {
		t.Skip(err)
	}
	p := filepath.Join(t.TempDir(), "vshell")
	os.WriteFile(p, minimalELF(elf.ET_EXEC, elf.EM_X86_64), 0o755)
	_, err := Build(Options{VShell: p, Version: "v1", Out: t.TempDir(), Fetch: func(string) (io.ReadCloser, error) { return nil, os.ErrNotExist }})
	if err == nil || !strings.Contains(err.Error(), "arm64") {
		t.Errorf("%v", err)
	}
}

func readInitramfs(t *testing.T, path string) []cpioEntry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	entries, err := readCpio(gz)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return entries
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

func TestStockPinned(t *testing.T) {
	for _, p := range Stock.all() {
		if len(p.SHA256) != 64 || len(p.URLs) == 0 || !strings.HasSuffix(p.URLs[0], ".deb") || p.Version == "" {
			t.Errorf("%s is not pinned: %+v", p.Name, p)
		}
	}
}
