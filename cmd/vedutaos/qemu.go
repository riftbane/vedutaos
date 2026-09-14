package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/riftbane/vedutaos/provision"
)

// The emulated screen is 4:3 and four times the panel each way, so with VEDUTA_SCALE=4 the
// console renders exactly the panel's 320×240.
const (
	screenW = 1280
	screenH = 960
)

// qemuConfig is everything the QEMU command line is made from.
type qemuConfig struct {
	Firmware string   // UEFI firmware for the virt machine
	Disk     string   // the machine's disk: an overlay on Debian's image
	Card     string   // the card folder, shown to the machine as a FAT disk
	Accel    string   // tcg, kvm or hvf
	CPUs     int      // virtual processors
	Memory   string   // guest memory, as QEMU spells it
	SSHPort  int      // host port forwarded to the machine's ssh, on loopback only
	Display  string   // QEMU's display backend, empty for its default
	Extra    []string // anything after "--"
}

// qemuArgs returns the arguments of qemu-system-aarch64 for c.
func qemuArgs(c qemuConfig) []string {
	args := []string{"-machine", "virt"}
	switch c.Accel {
	case "kvm", "hvf":
		args = append(args, "-accel", c.Accel, "-cpu", "host")
	default:
		args = append(args, "-accel", "tcg,thread=multi", "-cpu", "cortex-a72")
	}
	args = append(args,
		"-smp", strconv.Itoa(c.CPUs),
		"-m", c.Memory,
		"-bios", c.Firmware,
		// -blockdev rather than -drive file=: a Windows path's drive letter would read as a
		// protocol there.
		"-blockdev", "driver=qcow2,node-name=disk,file.driver=file,file.filename="+qemuEscape(c.Disk),
		"-device", "virtio-blk-pci,drive=disk,bootindex=0",
		"-blockdev", "driver=vvfat,node-name=card,dir="+qemuEscape(c.Card)+",label="+provision.Label+",read-only=on",
		"-device", "virtio-blk-pci,drive=card",
		"-device", fmt.Sprintf("virtio-gpu-pci,xres=%d,yres=%d", screenW, screenH),
		"-device", "qemu-xhci",
		"-device", "usb-kbd",
		"-netdev", fmt.Sprintf("user,id=net0,hostfwd=tcp:127.0.0.1:%d-:22", c.SSHPort),
		// No option ROM: the firmware never boots from the network, and not every QEMU
		// installation ships the ROM file.
		"-device", "virtio-net-pci,netdev=net0,romfile=",
		"-serial", "mon:stdio",
	)
	if c.Display != "" {
		args = append(args, "-display", c.Display)
	}
	return append(args, c.Extra...)
}

// qemuEscape doubles the commas QEMU would otherwise read as the end of a value.
func qemuEscape(s string) string {
	return strings.ReplaceAll(s, ",", ",,")
}

func qemuCommand(args []string, stdout, stderr io.Writer) int {
	var extra []string
	for i, a := range args {
		if a == "--" {
			args, extra = args[:i], args[i+1:]
			break
		}
	}
	fs := flag.NewFlagSet("vedutaos qemu", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		s        cardSettings
		cardDir  = fs.String("card", "", "the card `folder` (default: in this user's cache directory)")
		fresh    = fs.Bool("fresh", false, "throw the machine's disk away and install the console again from Debian's image")
		display  = fs.String("display", "", "QEMU's display: sdl, gtk, none… (default: QEMU's own choice)")
		qemuBin  = fs.String("qemu", "", "the qemu-system-aarch64 `program` (default: found on PATH)")
		firmware = fs.String("firmware", "", "UEFI firmware `file` for the virt machine (default: found beside QEMU)")
		image    = fs.String("image", "", "Debian 13 generic arm64 `qcow2` to start from (default: downloaded once, checksum verified)")
		sshPort  = fs.Int("ssh-port", 2222, "host `port` forwarded to the machine's ssh, on 127.0.0.1 only")
		cpus     = fs.Int("cpus", 4, "virtual processors")
		memory   = fs.String("memory", "2G", "guest memory")
		printCmd = fs.Bool("print", false, "make the card and print the QEMU command, without downloading, creating the disk or running")
	)
	s.register(fs, provision.QEMU)
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: vedutaos qemu [flags] [-- qemu-args]\n\nMakes a card for QEMU, then boots Debian's arm64 image with it. The first boot installs\nthe console; later ones go straight to the dashboard.\n\n")
		fs.PrintDefaults()
	}
	rest, err := parse(fs, args)
	if err != nil {
		return 2
	}
	if len(rest) != 0 {
		fs.Usage()
		return 2
	}
	o, p, err := s.options(provision.QEMU)
	if err != nil {
		fmt.Fprintln(stderr, "vedutaos qemu:", err)
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "vedutaos qemu:", err)
		return 1
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		return fail(err)
	}
	cache = filepath.Join(cache, "vedutaos", "qemu")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return fail(err)
	}
	if *cardDir == "" {
		*cardDir = filepath.Join(cache, "card")
	}
	if *cardDir, err = filepath.Abs(*cardDir); err != nil {
		return fail(err)
	}
	// Find QEMU before touching anything, so a missing one is said at once.
	qemu, err := findQEMU(*qemuBin)
	if err != nil {
		return fail(err)
	}
	if *firmware == "" {
		if *firmware, err = findFirmware(qemu); err != nil {
			return fail(err)
		}
	}
	if err := makeCard(*cardDir, o, p, &s, stdout); err != nil {
		return fail(err)
	}

	base := *image
	if base == "" {
		base = filepath.Join(cache, imageName)
		if _, err := os.Stat(base); err != nil && !*printCmd {
			fmt.Fprintf(stdout, "\ndownloading %s (once)\n", imageBase+imageName)
			if err := fetchImage(imageBase, imageName, base, stdout); err != nil {
				return fail(err)
			}
		}
	}
	if base, err = filepath.Abs(base); err != nil {
		return fail(err)
	}
	disk := filepath.Join(cache, "console.qcow2")
	if *fresh {
		if err := os.Remove(disk); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fail(err)
		}
	}
	if _, err := os.Stat(disk); err != nil && !*printCmd {
		// An overlay keeps Debian's image as it was downloaded, so --fresh is instant.
		img := exec.Command(qemuImg(qemu), "create", "-q", "-f", "qcow2", "-F", "qcow2", "-b", base, disk, "16G")
		img.Stdout, img.Stderr = stdout, stderr
		if err := img.Run(); err != nil {
			return fail(fmt.Errorf("creating the machine's disk: %w", err))
		}
		fmt.Fprintln(stdout, "\nThe first boot installs the console: a few minutes under emulation, then the dashboard.")
	}

	cfg := qemuConfig{
		Firmware: *firmware, Disk: disk, Card: *cardDir, Accel: accelerator(),
		CPUs: *cpus, Memory: *memory, SSHPort: *sshPort, Display: *display, Extra: extra,
	}
	argv := qemuArgs(cfg)
	fmt.Fprintf(stdout, "\n%s %s\n", qemu, strings.Join(quoteAll(argv), " "))
	if *printCmd {
		return 0
	}
	fmt.Fprintf(stdout, "\nIn the window: arrows move, Enter starts a game, Ctrl+Q leaves it. Here: log in as %s\n(password %s), or Ctrl-A x to stop the machine. ssh -p %d %s@127.0.0.1 works too.\n\n",
		provision.User, provision.QEMUPassword, *sshPort, provision.User)
	cmd := exec.Command(qemu, argv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		return fail(err)
	}
	return 0
}

// quoteAll quotes the arguments that a shell would split, for a command a person can copy.
func quoteAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"'") {
			a = strconv.Quote(a)
		}
		out[i] = a
	}
	return out
}

// Seams for tests.
var (
	lookPath = exec.LookPath
	goos     = runtime.GOOS
	goarch   = runtime.GOARCH
	fileOK   = func(p string) bool { fi, err := os.Stat(p); return err == nil && !fi.IsDir() }
)

func findQEMU(named string) (string, error) {
	if named != "" {
		return named, nil
	}
	if p, err := lookPath("qemu-system-aarch64"); err == nil {
		return p, nil
	}
	if goos == "windows" {
		for _, p := range []string{`C:\Program Files\qemu\qemu-system-aarch64.exe`, `C:\msys64\ucrt64\bin\qemu-system-aarch64.exe`} {
			if fileOK(p) {
				return p, nil
			}
		}
	}
	return "", errors.New("qemu-system-aarch64 not found: install QEMU (on Windows from https://qemu.weilnetz.de/w64/) or pass --qemu")
}

// qemuImg is the qemu-img that came with a QEMU.
func qemuImg(qemu string) string {
	name := "qemu-img"
	if goos == "windows" {
		name += ".exe"
	}
	if p := filepath.Join(filepath.Dir(qemu), name); fileOK(p) {
		return p
	}
	if p, err := lookPath("qemu-img"); err == nil {
		return p
	}
	return name
}

// findFirmware looks for the virt machine's UEFI firmware where QEMU's own installers and
// the Linux distributions put it.
func findFirmware(qemu string) (string, error) {
	dir := filepath.Dir(qemu)
	candidates := []string{
		filepath.Join(dir, "share", "edk2-aarch64-code.fd"),                 // Windows installer
		filepath.Join(dir, "..", "share", "qemu", "edk2-aarch64-code.fd"),   // MSYS2, Homebrew, source builds
		filepath.Join(dir, "..", "share", "edk2", "aarch64", "QEMU_EFI.fd"), // some source builds
		"/usr/share/qemu/edk2-aarch64-code.fd",                              // Fedora, Arch
		"/usr/share/qemu-efi-aarch64/QEMU_EFI.fd",                           // Debian, Ubuntu
		"/usr/share/AAVMF/AAVMF_CODE.fd",                                    // Debian, Ubuntu
		"/usr/share/edk2/aarch64/QEMU_EFI.fd",                               // Fedora (edk2-aarch64)
		"/opt/homebrew/share/qemu/edk2-aarch64-code.fd",                     // Homebrew on Apple silicon
	}
	for _, c := range candidates {
		if fileOK(c) {
			return filepath.Clean(c), nil
		}
	}
	return "", errors.New("no UEFI firmware for QEMU's arm64 machine found (edk2-aarch64-code.fd or QEMU_EFI.fd; on Debian and Ubuntu install qemu-efi-aarch64); pass --firmware")
}

// accelerator picks hardware virtualisation when the host is itself arm64 and offers it;
// otherwise every instruction is emulated.
func accelerator() string {
	if goarch != "arm64" {
		return "tcg"
	}
	switch goos {
	case "linux":
		if f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0); err == nil {
			f.Close()
			return "kvm"
		}
	case "darwin":
		return "hvf"
	}
	return "tcg"
}
