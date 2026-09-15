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

	"github.com/riftbane/vedutaos/card"
	"github.com/riftbane/vedutaos/image"
)

// The emulated screen is 4:3 and four times the panel each way, so with VEDUTA_SCALE=4 the
// console renders exactly the panel's 320×240.
const (
	screenW = 1280
	screenH = 960
)

// qemuConfig is everything the QEMU command line is made from.
type qemuConfig struct {
	Kernel  string   // Debian's arm64 kernel, written beside the image by vedutaos image
	Initrd  string   // its initramfs: the console
	Card    string   // the card folder, shown to the machine as a disk labelled card.Label
	Accel   string   // tcg, kvm or hvf
	CPUs    int      // virtual processors
	Memory  string   // guest memory, as QEMU spells it
	Display string   // QEMU's display backend, empty for its default
	Extra   []string // anything after "--"
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
		// The kernel is booted directly, and the initramfs is the whole system: there is no
		// disk image, no boot loader and no root file system.
		"-kernel", c.Kernel,
		"-initrd", c.Initrd,
		// Kernel messages go to the screen and the serial port; /dev/console, where the
		// dashboard and the games write, is the serial port, as on the Pi.
		"-append", "console=tty1 console=ttyAMA0 "+strings.Join(image.CmdlineSettings, " "),
		// -blockdev rather than -drive file=: a Windows path's drive letter would read as a
		// protocol there.
		"-blockdev", "driver=vvfat,node-name=card,dir="+qemuEscape(c.Card)+",label="+card.Label+",read-only=on",
		// No option ROM: nothing boots from the disk, and not every QEMU installation ships
		// the ROM file.
		"-device", "virtio-blk-pci,drive=card,romfile=",
		"-device", fmt.Sprintf("virtio-gpu-pci,xres=%d,yres=%d", screenW, screenH),
		"-device", "qemu-xhci",
		"-device", "usb-kbd",
		// A tablet reports where the pointer is, which the player reads as the stick: the
		// middle of the window is rest. It follows the host pointer without grabbing it.
		"-device", "usb-tablet",
		"-serial", "mon:stdio",
		// No network: the console has none, and QEMU's default card wants an option ROM
		// that not every installation ships.
		"-nic", "none",
		// Switching the console off ends QEMU, and so does a kernel panic (which would
		// otherwise reboot, as it does on the Pi).
		"-no-reboot",
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
		kernel   = fs.String("kernel", "", "the `kernel` QEMU boots (default: this release's "+image.KernelName+", fetched once; in the source tree, out/"+image.KernelName+")")
		initrd   = fs.String("initrd", "", "its `initramfs`, which is the console (default: "+image.InitrdName+" beside the kernel)")
		cardDir  = fs.String("card", "", "the card `folder` (default: in this user's cache directory)")
		display  = fs.String("display", "", "QEMU's display: sdl, gtk, none… (default: QEMU's own choice)")
		qemuBin  = fs.String("qemu", "", "the qemu-system-aarch64 `program` (default: found on PATH)")
		cpus     = fs.Int("cpus", 4, "virtual processors")
		memory   = fs.String("memory", "1G", "guest memory")
		printCmd = fs.Bool("print", false, "make the card and print the QEMU command, without running it")
	)
	s.register(fs)
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: vedutaos qemu [flags] [-- qemu-args]\n\nBoots the console on QEMU's arm64 machine with a card holding the games named. It is the\nsame console a Raspberry Pi runs, on Debian's kernel; the panel is the only thing QEMU cannot show.\n\n")
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
	fail := func(err error) int {
		fmt.Fprintln(stderr, "vedutaos qemu:", err)
		return 1
	}
	if err := s.validate(); err != nil {
		fmt.Fprintln(stderr, "vedutaos qemu:", err)
		return 2
	}
	if s.scale == 0 {
		s.scale = 4
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		return fail(err)
	}
	cache = filepath.Join(cache, "vedutaos", "qemu")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return fail(err)
	}
	if *kernel == "" {
		if root, ok := sourceRoot(); ok && fileOK(filepath.Join(root, "out", image.KernelName)) {
			*kernel = filepath.Join(root, "out", image.KernelName)
		} else {
			dir, err := fetchRelease(version, []string{image.KernelName, image.InitrdName}, stdout)
			if err != nil {
				return fail(err)
			}
			*kernel = filepath.Join(dir, image.KernelName)
		}
	}
	if *kernel, err = filepath.Abs(*kernel); err != nil {
		return fail(err)
	}
	if *initrd == "" {
		*initrd = filepath.Join(filepath.Dir(*kernel), image.InitrdName)
	}
	for _, f := range []string{*kernel, *initrd} {
		if !fileOK(f) {
			return fail(fmt.Errorf("%s is missing", f))
		}
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
	if err := makeCard(*cardDir, &s, stdout); err != nil {
		return fail(err)
	}
	if err := describe(stdout, *cardDir); err != nil {
		return fail(err)
	}

	cfg := qemuConfig{
		Kernel: *kernel, Initrd: *initrd, Card: *cardDir, Accel: accelerator(),
		CPUs: *cpus, Memory: *memory, Display: *display, Extra: extra,
	}
	argv := qemuArgs(cfg)
	fmt.Fprintf(stdout, "\n%s %s\n", qemu, strings.Join(quoteAll(argv), " "))
	if *printCmd {
		return 0
	}
	fmt.Fprint(stdout, "\nIn the window: arrows move, Enter starts a game, the mouse is the stick, Ctrl+Q leaves\na game and, on the dashboard, switches the console off.\nHere: the console's serial port. Ctrl-A x stops the machine at once.\n\n")
	cmd := exec.Command(qemu, argv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	tieToParent(cmd)
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
