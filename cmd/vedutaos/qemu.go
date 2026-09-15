package main

import (
	"encoding/binary"
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
	Kernel  string   // the image's Debian kernel, extracted beside it by vedutaos image
	Initrd  string   // its initramfs
	Root    string   // PARTUUID of the image's root partition
	Disk    string   // the machine's disk: an overlay on the image
	Card    string   // the card folder, shown to the machine as a disk labelled card.Label
	Accel   string   // tcg, kvm or hvf
	CPUs    int      // virtual processors
	Memory  string   // guest memory, as QEMU spells it
	SSHPort int      // host port forwarded to the machine's ssh, on loopback only
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
		// The kernel is booted directly: no UEFI, no boot loader in the image to keep.
		"-kernel", c.Kernel,
		"-initrd", c.Initrd,
		"-append", "root=PARTUUID="+c.Root+" rootfstype=ext4 rootwait console=ttyAMA0 "+strings.Join(image.CmdlineSettings, " "),
		// -blockdev rather than -drive file=: a Windows path's drive letter would read as a
		// protocol there.
		"-blockdev", "driver=qcow2,node-name=disk,file.driver=file,file.filename="+qemuEscape(c.Disk),
		"-device", "virtio-blk-pci,drive=disk",
		"-blockdev", "driver=vvfat,node-name=card,dir="+qemuEscape(c.Card)+",label="+card.Label+",read-only=on",
		"-device", "virtio-blk-pci,drive=card",
		"-device", fmt.Sprintf("virtio-gpu-pci,xres=%d,yres=%d", screenW, screenH),
		"-device", "qemu-xhci",
		"-device", "usb-kbd",
		// A tablet reports where the pointer is, which the player reads as the stick: the
		// middle of the window is rest. It follows the host pointer without grabbing it.
		"-device", "usb-tablet",
		"-netdev", fmt.Sprintf("user,id=net0,hostfwd=tcp:127.0.0.1:%d-:22", c.SSHPort),
		// No option ROM: nothing boots from the network, and not every QEMU installation
		// ships the ROM file.
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

// rootPartUUID reads the image's MBR disk identifier, which names its partitions to the
// kernel as PARTUUID=<id>-<n>; the root file system is the second partition.
func rootPartUUID(img string) (string, error) {
	f, err := os.Open(img)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var mbr [512]byte
	if _, err := io.ReadFull(f, mbr[:]); err != nil {
		return "", fmt.Errorf("%s: not a disk image: %w", img, err)
	}
	if mbr[510] != 0x55 || mbr[511] != 0xaa {
		return "", fmt.Errorf("%s: not a disk image (no MBR signature)", img)
	}
	id := binary.LittleEndian.Uint32(mbr[0x1b8:])
	if id == 0 {
		return "", fmt.Errorf("%s: the disk has no identifier", img)
	}
	return fmt.Sprintf("%08x-02", id), nil
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
		img      = fs.String("image", "", "the VedutaOS `image` to boot (default: out/"+image.ImageName+" in the source tree, else the one in this user's cache directory)")
		kernel   = fs.String("kernel", "", "the kernel QEMU boots (default: "+image.KernelName+" beside the image)")
		initrd   = fs.String("initrd", "", "its initramfs (default: "+image.InitrdName+" beside the image)")
		cardDir  = fs.String("card", "", "the card `folder` (default: in this user's cache directory)")
		fresh    = fs.Bool("fresh", false, "throw the machine's disk away: the next boot is the image's first")
		display  = fs.String("display", "", "QEMU's display: sdl, gtk, none… (default: QEMU's own choice)")
		qemuBin  = fs.String("qemu", "", "the qemu-system-aarch64 `program` (default: found on PATH)")
		sshPort  = fs.Int("ssh-port", 2222, "host `port` forwarded to the machine's ssh, on 127.0.0.1 only")
		cpus     = fs.Int("cpus", 4, "virtual processors")
		memory   = fs.String("memory", "1G", "guest memory")
		printCmd = fs.Bool("print", false, "make the card and print the QEMU command, without creating the disk or running")
	)
	s.register(fs)
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: vedutaos qemu [flags] [-- qemu-args]\n\nBoots the VedutaOS image on QEMU's arm64 machine with a card holding the games named.\nThe image is the one a Raspberry Pi gets; the panel is the only thing QEMU cannot show.\n\n")
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
	if *img == "" {
		if root, ok := sourceRoot(); ok && fileOK(filepath.Join(root, "out", image.ImageName)) {
			*img = filepath.Join(root, "out", image.ImageName)
		} else if fileOK(filepath.Join(cache, image.ImageName)) {
			*img = filepath.Join(cache, image.ImageName)
		} else {
			return fail(fmt.Errorf("no image: pass --image, or build one with \"vedutaos image\" on Linux (%s and %s must be beside it)", image.KernelName, image.InitrdName))
		}
	}
	if *img, err = filepath.Abs(*img); err != nil {
		return fail(err)
	}
	if *kernel == "" {
		*kernel = filepath.Join(filepath.Dir(*img), image.KernelName)
	}
	if *initrd == "" {
		*initrd = filepath.Join(filepath.Dir(*img), image.InitrdName)
	}
	for _, f := range []string{*img, *kernel, *initrd} {
		if !fileOK(f) {
			return fail(fmt.Errorf("%s is missing", f))
		}
	}
	root, err := rootPartUUID(*img)
	if err != nil {
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
	if err := makeCard(*cardDir, &s, stdout); err != nil {
		return fail(err)
	}
	if err := describe(stdout, *cardDir); err != nil {
		return fail(err)
	}

	disk := filepath.Join(cache, "console.qcow2")
	if *fresh {
		if err := os.Remove(disk); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fail(err)
		}
	}
	if !fileOK(disk) && !*printCmd {
		// An overlay keeps the image as it was built, so --fresh is instant.
		cmd := exec.Command(qemuImg(qemu), "create", "-q", "-f", "qcow2", "-F", "raw", "-b", *img, disk)
		cmd.Stdout, cmd.Stderr = stdout, stderr
		if err := cmd.Run(); err != nil {
			return fail(fmt.Errorf("creating the machine's disk: %w", err))
		}
	}

	cfg := qemuConfig{
		Kernel: *kernel, Initrd: *initrd, Root: root, Disk: disk, Card: *cardDir, Accel: accelerator(),
		CPUs: *cpus, Memory: *memory, SSHPort: *sshPort, Display: *display, Extra: extra,
	}
	argv := qemuArgs(cfg)
	fmt.Fprintf(stdout, "\n%s %s\n", qemu, strings.Join(quoteAll(argv), " "))
	if *printCmd {
		return 0
	}
	fmt.Fprintf(stdout, "\nIn the window: arrows move, Enter starts a game, the mouse is the stick, Ctrl+Q leaves it.\nHere: Ctrl-A x stops the machine. With a key given by --ssh-key: ssh -p %d veduta@127.0.0.1\n\n", *sshPort)
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
