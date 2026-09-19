//go:build linux

package main

// The console as PID 1. The kernel starts this program with nothing mounted and no driver
// loaded, and it does what an operating system's init and its service manager would: it
// mounts the kernel's file systems, loads the modules the image put beside it, finds the
// card and mounts it read-only, keeps the text console off the panel, reaps the processes
// that come back to it, and switches the machine off when asked. Then it is the dashboard.
// Every step says what it did on the kernel log, which is the serial port; a card carrying
// vedutaos/debug gets a shell there and on tty2.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/riftbane/vedutaos/card"
	"github.com/riftbane/vedutaos/initramfs"
)

const (
	cardTimeout = 30 * time.Second       // how long init waits for a card before showing an empty dashboard
	stickGrace  = 2 * time.Second        // how long a stick is given to appear before the boot volume is taken
	cardPoll    = 500 * time.Millisecond // how often the block devices are looked at while waiting
	fbTimeout   = 10 * time.Second       // how long init waits for a framebuffer before starting anyway
	retryAfter  = 2 * time.Second        // pause before the dashboard is started again after it failed
)

// system is what init keeps track of.
type system struct {
	mu       sync.Mutex
	cardDev  string // the device mounted on the card mount point, empty while none is
	lastFail string // the last mount failure said, so it is said once
	shellsUp bool
}

func initMain() {
	s := &system{}
	s.mountAll()
	openStatus()
	s.stdio()
	rel, _ := os.ReadFile(initramfs.Release)
	say("VedutaOS %s, Linux %s, init pid 1", strings.TrimSpace(string(rel)), kernelRelease())
	setEnv(defaultEnv)
	go s.reap()
	go s.signals()
	s.loadModules()
	if !s.waitCard(cardTimeout) {
		say("no card yet: looking on for a volume %s or %s", card.Label, card.BootLabel)
		go s.keepLooking()
	}
	s.waitFramebuffer()
	tick = unbindFbcon
	unbindFbcon()
	s.dashboard()
}

// mountAll mounts what the kernel provides: /proc, /sys, the device nodes.
func (s *system) mountAll() {
	for _, m := range []struct{ src, dst, typ string }{
		{"proc", "/proc", "proc"},
		{"sysfs", "/sys", "sysfs"},
		{"devtmpfs", "/dev", "devtmpfs"},
	} {
		os.MkdirAll(m.dst, 0o755)
		if err := syscall.Mount(m.src, m.dst, m.typ, 0, ""); err != nil && !errors.Is(err, syscall.EBUSY) {
			fmt.Fprintf(os.Stderr, "vedutaos: mount %s: %v\n", m.dst, err)
		}
	}
	os.MkdirAll("/dev/pts", 0o755)
	syscall.Mount("devpts", "/dev/pts", "devpts", 0, "")
	os.MkdirAll("/tmp", 0o1777)
}

// stdio puts this program's, and so every game's, output on /dev/console: the serial port,
// the last console the kernel command line names.
func (s *system) stdio() {
	f, err := os.OpenFile("/dev/console", os.O_RDWR, 0)
	if err != nil {
		return
	}
	for fd := 0; fd <= 2; fd++ {
		syscall.Dup3(int(f.Fd()), fd, 0)
	}
	f.Close()
}

func kernelRelease() string {
	var u syscall.Utsname
	if err := syscall.Uname(&u); err != nil {
		return "?"
	}
	var b []byte
	for _, c := range u.Release {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}

// loadModules loads the image's modules in the order the build worked out. A module that
// fails is said and skipped: the console may still come up on what is built in.
func (s *system) loadModules() {
	b, err := os.ReadFile(initramfs.ModuleList)
	if err != nil {
		say("modules: %v", err)
		return
	}
	loaded, failed := 0, 0
	for _, p := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if p == "" {
			continue
		}
		err := finitModule(p)
		if err == nil || errors.Is(err, syscall.EEXIST) {
			loaded++
			continue
		}
		failed++
		say("module %s: %v", strings.TrimSuffix(filepath.Base(p), ".ko"), err)
	}
	say("modules: %d loaded, %d failed", loaded, failed)
}

var noParams = []byte{0}

func finitModule(path string) error {
	if sysFinitModule == 0 {
		return errors.New("not supported on this architecture")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, _, e := syscall.Syscall(sysFinitModule, f.Fd(), uintptr(unsafe.Pointer(&noParams[0])), 0)
	if e != 0 {
		return e
	}
	return nil
}

// waitCard mounts the card, waiting up to d for one to appear: a volume labelled
// card.Label as soon as there is one, else the boot volume once a stick has had its
// chance. It reports whether a card is mounted.
func (s *system) waitCard(d time.Duration) bool {
	start := time.Now()
	for {
		if s.mountCard(time.Since(start) >= stickGrace) {
			return true
		}
		if time.Since(start) >= d {
			return false
		}
		time.Sleep(cardPoll)
	}
}

func (s *system) keepLooking() {
	for !s.mountCard(true) {
		time.Sleep(4 * cardPoll)
	}
}

// mountCard looks for a card among the block devices and mounts the first it takes, then
// applies the card's settings and opens the shells if the card asks for them.
func (s *system) mountCard(acceptBoot bool) bool {
	dev, label := findCard(acceptBoot)
	if dev == "" {
		return false
	}
	writable, err := mountCardAt(dev)
	if err != nil {
		if msg := fmt.Sprintf("card %s (%s): mount: %v", dev, label, err); msg != s.lastFail {
			s.lastFail = msg
			say("%s", msg)
		}
		return false
	}
	s.mu.Lock()
	s.cardDev = dev
	s.mu.Unlock()
	say("card %s (%s) on %s", dev, label, initramfs.CardMount)
	if writable {
		saves := filepath.Join(initramfs.CardWrite, card.Saves)
		os.Setenv(initramfs.SavesEnv, saves)
		say("games save in %s", saves)
	} else {
		os.Unsetenv(initramfs.SavesEnv)
	}
	if b, err := os.ReadFile(cardPath(card.EnvFile)); err == nil {
		kv := parseEnv(b)
		setEnv(kv)
		var parts []string
		for _, e := range kv {
			parts = append(parts, e[0]+"="+e[1])
		}
		say("settings from the card: %s", strings.Join(parts, " "))
	}
	if fileOK(cardPath(card.DebugFile)) {
		say("the card asks for shells")
		s.shells()
	}
	return true
}

// mountCardAt mounts the card: read-write on CardWrite, where only games' saves are
// written, and bound read-only on CardMount, where everything else reads it. A card that
// cannot be written (a locked card, a read-only disk) is mounted read-only on CardMount
// alone, and games cannot save. vfat's flush option writes a file out when it is closed,
// so a save is on the card before the game goes on.
func mountCardAt(dev string) (writable bool, err error) {
	if err := os.MkdirAll(initramfs.CardWrite, 0o755); err == nil {
		if werr := syscall.Mount(dev, initramfs.CardWrite, "vfat", syscall.MS_NOATIME, "flush"); werr == nil {
			if err := syscall.Mount(initramfs.CardWrite, initramfs.CardMount, "", syscall.MS_BIND, ""); err == nil {
				if err := syscall.Mount("", initramfs.CardMount, "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_NOATIME, ""); err == nil {
					return true, nil
				}
				syscall.Unmount(initramfs.CardMount, 0)
			}
			syscall.Unmount(initramfs.CardWrite, 0)
		} else {
			say("card %s cannot be written (%v): games cannot save", dev, werr)
		}
	}
	return false, syscall.Mount(dev, initramfs.CardMount, "vfat", syscall.MS_RDONLY|syscall.MS_NOATIME, "")
}

func cardPath(rel string) string {
	return filepath.Join(initramfs.CardMount, filepath.FromSlash(rel))
}

// findCard returns the block device holding a card: the first volume labelled card.Label
// or, when acceptBoot, the first labelled card.BootLabel. Labels are read from the
// devices themselves; there is no udev to ask.
func findCard(acceptBoot bool) (dev, label string) {
	entries, err := os.ReadDir("/sys/class/block")
	if err != nil {
		return "", ""
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	boot := ""
	for _, name := range names {
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "zram") {
			continue
		}
		if b, err := os.ReadFile("/sys/class/block/" + name + "/size"); err != nil || strings.TrimSpace(string(b)) == "0" {
			continue
		}
		f, err := os.Open("/dev/" + name)
		if err != nil {
			continue
		}
		l, err := card.ReadLabel(f)
		f.Close()
		if err != nil {
			continue
		}
		switch l {
		case card.Label:
			return "/dev/" + name, l
		case card.BootLabel:
			if boot == "" {
				boot = "/dev/" + name
			}
		}
	}
	if acceptBoot && boot != "" {
		return boot, card.BootLabel
	}
	return "", ""
}

// waitFramebuffer gives the panel's driver time to register its framebuffer, and says
// which framebuffers there are: on a board that is the first thing to know.
func (s *system) waitFramebuffer() {
	deadline := time.Now().Add(fbTimeout)
	for {
		names, _ := filepath.Glob("/sys/class/graphics/fb[0-9]*")
		if len(names) > 0 {
			var parts []string
			for _, n := range names {
				b, _ := os.ReadFile(n + "/name")
				parts = append(parts, filepath.Base(n)+" "+strings.TrimSpace(string(b)))
			}
			say("framebuffers: %s", strings.Join(parts, ", "))
			return
		}
		if time.Now().After(deadline) {
			say("no framebuffer after %v", fbTimeout)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// unbindFbcon takes the framebuffer away from the text console, which would otherwise draw
// the kernel's messages and a cursor over the dashboard. It is called again whenever the
// dashboard rescans, since the console binds itself to a framebuffer that appears later.
func unbindFbcon() {
	names, _ := filepath.Glob("/sys/class/vtconsole/vtcon*")
	for _, n := range names {
		name, _ := os.ReadFile(n + "/name")
		if !strings.Contains(string(name), "frame buffer") {
			continue
		}
		if b, _ := os.ReadFile(n + "/bind"); strings.TrimSpace(string(b)) == "0" {
			continue
		}
		os.WriteFile(n+"/bind", []byte("0\n"), 0)
	}
}

// dashboard runs the dashboard for as long as the console is on: the one on the card when
// there is one, else the image's own, again after a failure, and the machine is switched
// off when the player leaves it. A dashboard that keeps failing gets the shells opened,
// so that a console with a broken screen or pad can still be looked at.
func (s *system) dashboard() {
	games := os.Getenv("VEDUTAOS_GAMES")
	failures, cardFailures := 0, 0
	for {
		var err error
		if cardShell := cardPath(card.VShell); cardFailures < 3 && fileOK(cardShell) {
			say("dashboard from the card")
			if err = s.runDashboard(cardShell); err != nil {
				cardFailures++
				if cardFailures == 3 {
					say("the card's dashboard does not run: using the image's")
				}
			}
		} else {
			err = run(games)
		}
		if err == nil {
			s.halt(syscall.LINUX_REBOOT_CMD_POWER_OFF, "the dashboard was left")
		}
		failures++
		say("dashboard: %v", err)
		if failures == 3 {
			say("the dashboard does not run: opening shells")
			s.shells()
		}
		time.Sleep(retryAfter)
	}
}

// runDashboard runs a dashboard program from the card, with this program's environment
// and its output, and waits for it.
func (s *system) runDashboard(path string) error {
	cmd := exec.Command(path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := startChild(cmd); err != nil {
		return err
	}
	return waitChild(cmd)
}

// halt switches the machine off, or reboots it, once the games and shells have gone.
func (s *system) halt(cmd int, why string) {
	what := "power off"
	if cmd == syscall.LINUX_REBOOT_CMD_RESTART {
		what = "reboot"
	}
	say("%s: %s", what, why)
	signalChildren(syscall.SIGTERM)
	time.Sleep(time.Second)
	signalChildren(syscall.SIGKILL)
	syscall.Sync()
	syscall.Unmount(initramfs.CardMount, 0)
	syscall.Unmount(initramfs.CardWrite, 0)
	syscall.Sync()
	syscall.Reboot(cmd)
	for {
		time.Sleep(time.Hour) // the kernel refused: nothing else can be done
	}
}

func signalChildren(sig syscall.Signal) {
	children.Lock()
	defer children.Unlock()
	for pid := range children.pids {
		syscall.Kill(pid, sig)
	}
}

// signals does what init is asked by signal: busybox's reboot sends TERM, its poweroff
// USR2 and its halt USR1; the kernel sends INT for Ctrl-Alt-Del.
func (s *system) signals() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1, syscall.SIGUSR2)
	for sig := range ch {
		switch sig {
		case syscall.SIGTERM, syscall.SIGINT:
			s.halt(syscall.LINUX_REBOOT_CMD_RESTART, "asked by signal")
		default:
			s.halt(syscall.LINUX_REBOOT_CMD_POWER_OFF, "asked by signal")
		}
	}
}

// reap waits for the processes that ended and had nobody to wait for them: a game's
// helper outliving the game, a shell's background job. What this program started itself
// is left to the code that started it.
func (s *system) reap() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGCHLD)
	t := time.NewTicker(5 * time.Second)
	for {
		select {
		case <-ch:
		case <-t.C:
		}
		for {
			pid := zombie()
			if pid <= 0 || isChild(pid) {
				break
			}
			syscall.Wait4(pid, nil, 0, nil)
		}
	}
}

// zombie returns a child that has ended and not been waited for, without taking it, or 0.
func zombie() int {
	const pAll, wNoHang, wExited, wNoWait = 0, 1, 4, 0x01000000
	var info [128]byte
	_, _, e := syscall.Syscall6(syscall.SYS_WAITID, pAll, 0, uintptr(unsafe.Pointer(&info[0])), wExited|wNoHang|wNoWait, 0, 0)
	if e != 0 {
		return 0
	}
	return int(*(*int32)(unsafe.Pointer(&info[16])))
}

// shells opens a shell on the serial port and one on tty2 (Alt+F2 on a keyboard), each
// started again when it exits. busybox provides the shell and its tools.
func (s *system) shells() {
	s.mu.Lock()
	up := s.shellsUp
	s.shellsUp = true
	s.mu.Unlock()
	if up {
		return
	}
	if out, err := exec.Command(initramfs.Busybox, "--install", "-s", "/bin").CombinedOutput(); err != nil {
		say("busybox --install: %v %s", err, strings.TrimSpace(string(out)))
	}
	for _, tty := range []string{serialTTY(), "/dev/tty2"} {
		go s.respawn(tty)
	}
}

// serialTTY is the device of the serial console: the active console that is not a
// virtual terminal, /dev/console when there is none.
func serialTTY() string {
	b, _ := os.ReadFile("/sys/class/tty/console/active")
	for _, name := range strings.Fields(string(b)) {
		if rest := strings.TrimPrefix(name, "tty"); rest != "" && (rest[0] < '0' || rest[0] > '9') {
			return "/dev/" + name
		}
	}
	return "/dev/console"
}

func (s *system) respawn(tty string) {
	for {
		f, err := os.OpenFile(tty, os.O_RDWR, 0)
		if err != nil {
			say("shell on %s: %v", tty, err)
			time.Sleep(10 * time.Second)
			continue
		}
		fmt.Fprintf(f, "\r\nVedutaOS %s shell on %s; exit starts a new one.\r\n", version, tty)
		cmd := exec.Command("/bin/sh")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = f, f, f
		cmd.Env = append(os.Environ(), "PATH=/bin", "HOME=/", "PS1=vedutaos# ")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
		if err := startChild(cmd); err != nil {
			say("shell on %s: %v", tty, err)
		} else {
			waitChild(cmd)
		}
		f.Close()
		time.Sleep(time.Second)
	}
}
