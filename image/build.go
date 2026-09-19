// Package image builds the VedutaOS image: one FAT32 volume holding the Raspberry Pi boot
// firmware and kernels, Armbian's kernel for the Orange Pi Zero 2W with its U-Boot before
// the volume (sunxi.go), an initramfs for each kernel with the console in it, and the games.
// There is no root file system and no distribution: the initramfs is the system, and the
// dashboard is its init. The same console, in an initramfs for Debian's arm64 kernel,
// boots on QEMU's virt machine, which is how it is tested.
//
// Everything comes from seven pinned Debian packages (packages.go), unpacked without
// installing anything. Building needs xz and mtools, no root and no emulator, and runs on
// any Linux or macOS machine, or a GitHub Actions runner.
package image

import (
	"bytes"
	"compress/gzip"
	"debug/elf"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/riftbane/vedutaos/card"
	"github.com/riftbane/vedutaos/initramfs"
	"github.com/riftbane/vedutaos/install"
	"github.com/riftbane/vedutaos/panel"
)

// Options say what to build.
type Options struct {
	Packages Packages // Stock when zero
	VShell   string   // the console: dashboard and init in one program, built for linux/arm64
	Version  string   // written to Release in the initramfs and vedutaos/release on the card
	Out      string   // directory for vedutaos.img, vmlinuz and initrd.img
	Cache    string   // directory keeping the packages; Out when empty
	Size     int64    // the image's size in bytes; DefaultSize when zero
	Panel    panel.Options
	Wiring   Wiring // DefaultPins and DefaultSPISpeed when zero
	Fetch    Getter // how packages are downloaded; HTTP when nil
	Verbose  io.Writer
	// Games are put into the card's games folder: game folders, release archives or Veduta
	// projects, as vedutaos card takes them, so a written card plays at once.
	Games []string
	Build install.Builder // compiles a project's game; install.GoBuild when nil
}

// Result names what Build wrote.
type Result struct {
	Image, Kernel, Initrd string
}

// What Build writes.
const (
	ImageName   = "vedutaos.img"
	KernelName  = "vmlinuz"    // Debian's arm64 kernel, for QEMU
	InitrdName  = "initrd.img" // its initramfs
	DefaultSize = 2 << 30
)

// Modules each kernel gets, by name. What a kernel builds in is left out on its own.
var (
	// commonModules are what every console needs: the card's file system, a USB pad or
	// keyboard as event devices.
	commonModules = []string{"vfat", "nls_cp437", "nls_ascii", "xhci-pci", "usbhid", "hid-generic", "evdev"}
	// piModules drive the panel and the buttons: SPI on the boards up to the Pi 4 and on
	// the Pi 5, the MIPI DBI panel driver, a backlight on a GPIO or PWM line, and the
	// buttons of the gpio-key overlays (a module on the Pi kernels, not built in). Then the
	// Wi-Fi: brcmfmac and its vendor modules, which it would otherwise ask modprobe for,
	// and there is no modprobe.
	piModules = []string{"spi-bcm2835", "spi-dw-mmio", "panel-mipi-dbi", "gpio_backlight", "pwm_bl", "gpio_keys",
		"brcmfmac", "brcmfmac_wcc", "brcmfmac_cyw", "brcmfmac_bca"}
	// virtModules are QEMU's disk and screen, and two simulated radios for the Wi-Fi, with
	// the ciphers mac80211 does WPA2 with in software (CCMP, and CMAC for protected
	// management frames), which it would otherwise ask modprobe for. The Pis' radio
	// ciphers in its own firmware.
	virtModules = []string{"virtio_blk", "virtio-gpu", "mac80211_hwsim", "ccm", "cmac"}
)

// piKernels are the Pi kernels and the names the boot firmware looks for.
var piKernels = []struct {
	pick              func(Packages) Package
	kernel, initramfs string
}{
	{func(p Packages) Package { return p.KernelV8 }, "kernel8.img", "initramfs8"},
	{func(p Packages) Package { return p.Kernel2712 }, "kernel_2712.img", "initramfs_2712"},
}

// Check reports why this machine cannot build an image, or nil.
func Check() error {
	if runtime.GOOS == "windows" {
		return errors.New("vedutaos image: builds on Linux or macOS (a GitHub Actions runner does)")
	}
	for _, t := range []string{"xz", "mformat", "mcopy"} {
		if _, err := exec.LookPath(t); err != nil {
			return fmt.Errorf("vedutaos image: %s is not installed (xz-utils, mtools)", t)
		}
	}
	return nil
}

// Build makes the image.
func Build(o Options) (Result, error) {
	var r Result
	if err := Check(); err != nil {
		return r, err
	}
	if o.Packages.Firmware.Name == "" {
		o.Packages = Stock
	}
	if o.Cache == "" {
		o.Cache = o.Out
	}
	if o.Size == 0 {
		o.Size = DefaultSize
	}
	if o.Wiring.Speed == 0 {
		o.Wiring.Speed = DefaultSPISpeed
	}
	if o.Wiring.Pins == (Pins{}) {
		o.Wiring.Pins = DefaultPins
	}
	if err := o.Wiring.Pins.Check(); err != nil {
		return r, err
	}
	if o.Fetch == nil {
		o.Fetch = httpGet
	}
	if o.Verbose == nil {
		o.Verbose = io.Discard
	}
	if o.VShell == "" || o.Version == "" || o.Out == "" {
		return r, errors.New("image: VShell, Version and Out are required")
	}
	if o.Size%sectorSize != 0 || o.Size < 64<<20 {
		return r, errors.New("image: Size must be a multiple of 512 bytes and at least 64 MiB")
	}
	for _, d := range []string{o.Out, o.Cache} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return r, err
		}
	}
	vshell, err := os.ReadFile(o.VShell)
	if err != nil {
		return r, err
	}
	if err := checkARM64(vshell); err != nil {
		return r, fmt.Errorf("%s: %w", o.VShell, err)
	}
	fw, err := panelFirmware(o.Panel)
	if err != nil {
		return r, err
	}

	// The packages, fetched and unpacked once into the cache.
	roots := map[string]string{}
	for _, p := range o.Packages.all() {
		deb, err := fetchPackage(p, filepath.Join(o.Cache, "debs"), o.Fetch, o.Verbose)
		if err != nil {
			return r, err
		}
		root := filepath.Join(o.Cache, "pkg", p.Name)
		fmt.Fprintf(o.Verbose, "%s: unpacking\n", p.Name)
		if err := unpackDeb(deb, root); err != nil {
			return r, err
		}
		roots[p.Name] = root
	}
	busybox, err := os.ReadFile(filepath.Join(roots[o.Packages.Busybox.Name], "usr", "bin", "busybox"))
	if err != nil {
		return r, err
	}
	in := initramfsInput{vshell: vshell, busybox: busybox, firmware: fw, version: o.Version}

	// The Wi-Fi: wpa_supplicant for every initramfs but the Orange Pi's, whose radio has
	// no driver yet, and the radios' firmware for the Raspberry Pis'.
	var wifiRoots []string
	for _, p := range o.Packages.WiFi {
		wifiRoots = append(wifiRoots, roots[p.Name])
	}
	supplicant, err := supplicantTree(wifiRoots)
	if err != nil {
		return r, err
	}
	piFirmware, err := piFirmwareTree(roots[o.Packages.WiFiFirmware.Name])
	if err != nil {
		return r, err
	}
	piExtra := newTree().merge(supplicant).merge(piFirmware)

	stage, err := os.MkdirTemp("", "vedutaos-card-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(stage)

	// The boot firmware, for the boards whose boot ROM needs it (the Pi 5 has its own).
	if err := copyGlob(filepath.Join(roots[o.Packages.Firmware.Name], "usr", "lib", "raspi-firmware", "*"), stage); err != nil {
		return r, err
	}
	var releases []string
	for _, k := range piKernels {
		p := k.pick(o.Packages)
		root := roots[p.Name]
		mods, err := loadModules(root)
		if err != nil {
			return r, err
		}
		releases = append(releases, mods.release)
		fmt.Fprintf(o.Verbose, "%s: %s and its initramfs\n", p.Name, k.kernel)
		if err := copyFile(filepath.Join(root, "boot", "vmlinuz-"+mods.release), filepath.Join(stage, k.kernel)); err != nil {
			return r, err
		}
		// Every kernel package carries the device trees of every board; the later one wins,
		// as when both are installed.
		if err := copyGlob(filepath.Join(mods.dir, "dtb", "broadcom", "bcm27*.dtb"), stage); err != nil {
			return r, err
		}
		if err := copyGlob(filepath.Join(mods.dir, "dtb", "overlays", "*"), filepath.Join(stage, "overlays")); err != nil {
			return r, err
		}
		if err := writeInitramfs(filepath.Join(stage, k.initramfs), mods, append(append([]string{}, commonModules...), piModules...), in, piExtra); err != nil {
			return r, fmt.Errorf("%s: %w", p.Name, err)
		}
	}

	// The Orange Pi Zero 2W: Armbian's kernel, the board's tree with the console written in,
	// the initramfs, the boot menu, and U-Boot, which goes before the volume.
	sunxi := roots[o.Packages.KernelSunxi.Name]
	smods, err := loadModules(sunxi)
	if err != nil {
		return r, err
	}
	releases = append(releases, smods.release)
	fmt.Fprintf(o.Verbose, "%s: %s/Image, its initramfs and %s\n", o.Packages.KernelSunxi.Name, SunxiDir, Zero2WDTB)
	if err := copyFile(filepath.Join(sunxi, "boot", "vmlinuz-"+smods.release), filepath.Join(stage, SunxiDir, "Image")); err != nil {
		return r, err
	}
	dtb, err := os.ReadFile(filepath.Join(sunxi, "usr", "lib", "linux-image-"+smods.release, "allwinner", Zero2WDTB))
	if err != nil {
		return r, err
	}
	if dtb, err = Zero2WTree(dtb, o.Wiring); err != nil {
		return r, err
	}
	if err := os.WriteFile(filepath.Join(stage, SunxiDir, Zero2WDTB), dtb, 0o644); err != nil {
		return r, err
	}
	if err := writeInitramfs(filepath.Join(stage, SunxiDir, "initrd.img"), smods, sunxiModules, in, nil); err != nil {
		return r, fmt.Errorf("%s: %w", o.Packages.KernelSunxi.Name, err)
	}
	if err := os.MkdirAll(filepath.Join(stage, "extlinux"), 0o755); err != nil {
		return r, err
	}
	if err := os.WriteFile(filepath.Join(stage, "extlinux", "extlinux.conf"), ExtlinuxConf(), 0o644); err != nil {
		return r, err
	}
	ubootFiles, _ := filepath.Glob(filepath.Join(roots[o.Packages.UBootZero2W.Name], "usr", "lib", "linux-u-boot-*", "u-boot-sunxi-with-spl.bin"))
	if len(ubootFiles) != 1 {
		return r, fmt.Errorf("%s: want one u-boot-sunxi-with-spl.bin, found %d", o.Packages.UBootZero2W.Name, len(ubootFiles))
	}
	uboot, err := os.ReadFile(ubootFiles[0])
	if err != nil {
		return r, err
	}
	if err := checkUBoot(uboot); err != nil {
		return r, err
	}

	// Debian's kernel, beside the image rather than in it: QEMU boots it directly.
	virt := roots[o.Packages.KernelVirt.Name]
	mods, err := loadModules(virt)
	if err != nil {
		return r, err
	}
	fmt.Fprintf(o.Verbose, "%s: %s and %s for QEMU\n", o.Packages.KernelVirt.Name, KernelName, InitrdName)
	r.Kernel = filepath.Join(o.Out, KernelName)
	r.Initrd = filepath.Join(o.Out, InitrdName)
	if err := copyFile(filepath.Join(virt, "boot", "vmlinuz-"+mods.release), r.Kernel); err != nil {
		return r, err
	}
	if err := writeInitramfs(r.Initrd, mods, append(append([]string{}, commonModules...), virtModules...), in, supplicant); err != nil {
		return r, fmt.Errorf("%s: %w", o.Packages.KernelVirt.Name, err)
	}

	// The card's own files.
	if err := os.WriteFile(filepath.Join(stage, "config.txt"), ConfigTxt(nil, &o.Wiring), 0o644); err != nil {
		return r, err
	}
	if err := os.WriteFile(filepath.Join(stage, "overlays", ButtonsOverlay+".dtbo"), ButtonsDTBO(o.Wiring.Pins), 0o644); err != nil {
		return r, err
	}
	if err := os.WriteFile(filepath.Join(stage, "cmdline.txt"), CmdlineTxt(), 0o644); err != nil {
		return r, err
	}
	if err := os.MkdirAll(filepath.Join(stage, card.Games), 0o755); err != nil {
		return r, err
	}
	build := o.Build
	if build == nil {
		build = install.GoBuild
	}
	for _, g := range o.Games {
		dir, err := install.Game(filepath.Join(stage, card.Games), g, build)
		if err != nil {
			return r, fmt.Errorf("image: game %s: %w", g, err)
		}
		fmt.Fprintf(o.Verbose, "game %s\n", filepath.Base(dir))
	}
	if err := os.MkdirAll(filepath.Join(stage, card.Dir), 0o755); err != nil {
		return r, err
	}
	release := fmt.Sprintf("VedutaOS %s\nkernels %s, %s\nOrange Pi kernel %s\nQEMU kernel %s\n", o.Version, releases[0], releases[1], releases[2], mods.release)
	if err := os.WriteFile(filepath.Join(stage, filepath.FromSlash(card.ReleaseFile)), []byte(release), 0o644); err != nil {
		return r, err
	}

	// The image: sparse, so an empty 2 GB costs nothing on disk and nothing in the .xz.
	r.Image = filepath.Join(o.Out, ImageName)
	tmp := r.Image + ".part"
	fmt.Fprintf(o.Verbose, "writing %s (%d MiB, FAT32 %s)\n", r.Image, o.Size>>20, card.BootLabel)
	if err := writeImage(tmp, o.Size, stage, o.Version, uboot); err != nil {
		os.Remove(tmp)
		return r, err
	}
	if err := os.Rename(tmp, r.Image); err != nil {
		return r, err
	}
	return r, nil
}

// writeImage makes the image file: the partition table, U-Boot for the Allwinner boot ROM
// in the gap before the partition, the formatted volume, the files.
func writeImage(path string, size int64, stage, version string, uboot []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		return err
	}
	h := fnv.New32a()
	h.Write([]byte(version))
	sectors := uint32(size / sectorSize)
	if _, err := f.WriteAt(mbr(sectors, h.Sum32()), 0); err != nil {
		f.Close()
		return err
	}
	if _, err := f.WriteAt(uboot, ubootOffset); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := formatFAT(path, sectors, card.BootLabel); err != nil {
		return err
	}
	return copyToFAT(path, stage)
}

// initramfsInput is what every initramfs holds besides its kernel's modules.
type initramfsInput struct {
	vshell, busybox, firmware []byte
	version                   string
}

// writeInitramfs writes a gzip-compressed cpio holding the console for one kernel: init
// (the dashboard), busybox, the panel's firmware, the modules named with everything they
// need, the order to load them in, and the extra files (the Wi-Fi), when there are any.
func writeInitramfs(path string, mods *modules, names []string, in initramfsInput, extra *tree) error {
	order, err := mods.resolve(names)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	c := newCpio(gz)
	dirs := map[string]bool{}
	dir := func(d string) {
		d = strings.TrimPrefix(d, "/")
		if d == "" || dirs[d] {
			return
		}
		dirs[d] = true
		c.dir(d)
	}
	for _, d := range []string{"bin", "dev", "etc", "lib", initramfs.Firmware, "lib/modules", "lib/modules/" + mods.release, "proc", "sys", "tmp", "boot", initramfs.CardMount, initramfs.CardMount + "/" + card.Games} {
		dir(d)
	}
	c.file(initramfs.Init, 0o755, in.vshell)
	c.file(initramfs.Busybox, 0o755, in.busybox)
	c.file(initramfs.Firmware+"/"+panel.FirmwareName, 0o644, in.firmware)
	c.file(initramfs.Release, 0o644, []byte(in.version+"\n"))
	var list strings.Builder
	for _, name := range order {
		b, err := mods.read(name)
		if err != nil {
			return err
		}
		p := mods.plainPath(name)
		var parents []string
		for d := filepath.Dir(p); d != "/lib/modules/"+mods.release; d = filepath.Dir(d) {
			parents = append([]string{d}, parents...)
		}
		for _, d := range parents {
			dir(d)
		}
		c.file(p, 0o644, b)
		list.WriteString(p + "\n")
	}
	c.file(initramfs.ModuleList, 0o644, []byte(list.String()))
	if extra != nil {
		for _, p := range extra.paths() {
			var parents []string
			for d := filepath.Dir(p); d != "/"; d = filepath.Dir(d) {
				parents = append([]string{d}, parents...)
			}
			for _, d := range parents {
				dir(d)
			}
			if target, ok := extra.links[p]; ok {
				c.symlink(p, target)
			} else {
				c.file(p, extra.perms[p], extra.files[p])
			}
		}
	}
	if err := c.close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Close()
}

// panelFirmware is the panel's start-up sequence, as the kernel's driver loads it.
func panelFirmware(o panel.Options) ([]byte, error) {
	cmds, err := panel.ILI9341(o)
	if err != nil {
		return nil, err
	}
	return panel.Encode(cmds)
}

// checkARM64 refuses a console program that the boards could not run.
func checkARM64(b []byte) error {
	f, err := elf.NewFile(bytes.NewReader(b))
	if err != nil {
		return errors.New("not a Linux program")
	}
	defer f.Close()
	if f.Machine != elf.EM_AARCH64 {
		return fmt.Errorf("built for %s, not arm64", strings.TrimPrefix(f.Machine.String(), "EM_"))
	}
	if f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN {
		return errors.New("not an executable")
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			return errors.New("not statically linked (build it with CGO_ENABLED=0)")
		}
	}
	return nil
}

func copyGlob(pattern, dstDir string) error {
	names, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("nothing matches %s", pattern)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	for _, n := range names {
		if fi, err := os.Stat(n); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if err := copyFile(n, filepath.Join(dstDir, filepath.Base(n))); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
