//go:build linux

// Package e2e boots the console on QEMU and drives it as a player would: init brings the
// dashboard up, the dashboard lists the card's game, starts it on A, comes back on Home,
// and switches the machine off when it is left. The console says what it does on the
// serial port, and that is what the test reads. It is the test of the image itself, so it
// runs only when asked:
//
//	VEDUTAOS_E2E=1 go test ./test/e2e -timeout 40m
//
// It needs out/vmlinuz and out/initrd.img (vedutaos image) and QEMU. Under emulation the
// boot takes a few minutes.
package e2e

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

var update = flag.Bool("update-golden", false, "rewrite the reference screens")

const scale = 4 // VEDUTA_SCALE in QEMU: the 1280×960 screen shows the 320×240 panel four times

type console struct {
	t       *testing.T
	qemu    *exec.Cmd
	monitor string // unix socket of QEMU's monitor
	logPath string
	done    chan struct{} // closed when QEMU has exited
	exit    error
}

func TestConsole(t *testing.T) {
	if os.Getenv("VEDUTAOS_E2E") == "" {
		t.Skip("set VEDUTAOS_E2E=1 to boot the console on QEMU")
	}
	if _, err := exec.LookPath("qemu-system-aarch64"); err != nil {
		t.Fatal("qemu-system-aarch64 is not installed")
	}
	kernel := os.Getenv("VEDUTAOS_KERNEL")
	if kernel == "" {
		kernel = filepath.Join("..", "..", "out", "vmlinuz")
	}
	if k, err := filepath.Abs(kernel); err == nil {
		kernel = k
	}
	for _, f := range []string{kernel, filepath.Join(filepath.Dir(kernel), "initrd.img")} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("no %s: build the image with vedutaos image, or set VEDUTAOS_KERNEL", f)
		}
	}

	work := t.TempDir()
	tool := filepath.Join(work, "vedutaos")
	build := exec.Command("go", "build", "-o", tool, "../../cmd/vedutaos")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the tool: %v\n%s", err, out)
	}
	engine, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/riftbane/veduta").Output()
	if err != nil {
		t.Fatalf("finding the engine: %v", err)
	}
	demo := filepath.Join(strings.TrimSpace(string(engine)), "template")
	monitor := filepath.Join(work, "monitor.sock")
	logf, err := os.Create(filepath.Join(work, "serial.log"))
	if err != nil {
		t.Fatal(err)
	}
	c := &console{t: t, monitor: monitor, logPath: logf.Name(), done: make(chan struct{})}

	// The tool makes the card and boots the console as it would for a person, with the
	// screen and the monitor moved where the test can reach them.
	c.qemu = exec.Command(tool, "qemu", "--kernel", kernel, "--card", filepath.Join(work, "card"), "--game", demo,
		"--", "-display", "none", "-monitor", "unix:"+monitor+",server,nowait")
	c.qemu.Env = append(os.Environ(), "XDG_CACHE_HOME="+filepath.Join(work, "cache"), "HOME="+work)
	c.qemu.Stdout, c.qemu.Stderr = logf, logf
	// The tool and QEMU share a process group, so stopping the test stops the machine.
	c.qemu.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.qemu.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		c.exit = c.qemu.Wait()
		close(c.done)
	}()
	t.Cleanup(func() {
		syscall.Kill(-c.qemu.Process.Pid, syscall.SIGKILL)
		<-c.done
		logf.Close()
		if t.Failed() {
			b, _ := os.ReadFile(logf.Name())
			t.Logf("serial output:\n%s", tail(b, 20000))
		}
	})

	t.Log("waiting for the console to boot")
	c.waitLog(25*time.Minute, "the dashboard", `vedutaos: dashboard: 1 game`, 1)
	log := c.log()
	for _, want := range []string{`vedutaos: modules: \d+ loaded, 0 failed`, `vedutaos: card /dev/\S+ \(VEDUTA\) on /boot/firmware`, `vedutaos: settings from the card: VEDUTA_SCALE=4`, `vedutaos: framebuffers: fb0 virtio_gpudrmfb`} {
		if !regexp.MustCompile(want).MatchString(log) {
			t.Errorf("the serial log lacks %s", want)
		}
	}

	// The dashboard lists the game.
	time.Sleep(3 * time.Second)
	list := c.screen()
	c.golden("list", list)

	// A starts it; the game draws something else. The key is pressed again while waiting:
	// the emulated keyboard drops one now and then.
	c.press("spc", 2*time.Minute, "the game to start", `vedutaos: game \S+ started`, 1)
	time.Sleep(5 * time.Second)
	if game := c.screen(); same(game, list) {
		c.save("game.got", game)
		t.Error("the screen did not change when the game started")
	}

	// Home ends it; the dashboard is back as it was.
	c.press("ctrl-q", 2*time.Minute, "the game to end", `vedutaos: game \S+ ended`, 1)
	c.waitLog(time.Minute, "the dashboard again", `vedutaos: dashboard: 1 game`, 2)
	time.Sleep(3 * time.Second)
	c.golden("list", c.screen())

	// Leaving the dashboard switches the console off, and the machine with it.
	c.press("esc", 2*time.Minute, "the console to switch off", `vedutaos: power off`, 1)
	select {
	case <-c.done:
		if c.exit != nil {
			t.Errorf("QEMU did not end cleanly: %v", c.exit)
		}
	case <-time.After(2 * time.Minute):
		t.Error("QEMU is still running two minutes after the console switched off")
	}
}

// press presses a key, and again every fifteen seconds, until the log has the line.
func (c *console) press(key string, d time.Duration, what, line string, count int) {
	c.t.Helper()
	re := regexp.MustCompile("(?m)" + line)
	last := time.Now()
	c.key(key)
	c.waitFor(d, what, func() bool {
		if len(re.FindAllString(c.log(), -1)) >= count {
			return true
		}
		if time.Since(last) > 15*time.Second {
			c.key(key)
			last = time.Now()
		}
		return false
	})
}

// waitLog waits until the serial log has had the line count times.
func (c *console) waitLog(d time.Duration, what, line string, count int) {
	c.t.Helper()
	re := regexp.MustCompile("(?m)" + line)
	c.waitFor(d, what, func() bool { return len(re.FindAllString(c.log(), -1)) >= count })
}

func (c *console) log() string {
	b, _ := os.ReadFile(c.logPath)
	return string(b)
}

func (c *console) waitFor(d time.Duration, what string, ok func() bool) {
	c.t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			c.t.Fatalf("waited %v for %s", d, what)
		}
		select {
		case <-c.done:
			// The machine may have ended on the very line waited for: switching off.
			if ok() {
				return
			}
			c.t.Fatalf("QEMU exited (%v) while waiting for %s", c.exit, what)
		case <-time.After(2 * time.Second):
		}
	}
}

// key presses a key on the emulated keyboard, in QEMU's monitor spelling.
func (c *console) key(name string) {
	c.t.Helper()
	c.monitorCmd("sendkey " + name)
}

func (c *console) monitorCmd(cmd string) string {
	c.t.Helper()
	conn, err := net.DialTimeout("unix", c.monitor, 10*time.Second)
	if err != nil {
		c.t.Fatalf("QEMU monitor: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(conn)
	prompt := func() string {
		var b strings.Builder
		for {
			s, err := r.ReadString(')')
			b.WriteString(s)
			if err != nil || strings.HasSuffix(b.String(), "(qemu)") {
				return b.String()
			}
		}
	}
	prompt()
	fmt.Fprintf(conn, "%s\n", cmd)
	return prompt()
}

// screen is what the panel shows: the emulated screen taken down to the panel's size.
func (c *console) screen() *image.RGBA {
	c.t.Helper()
	ppm := filepath.Join(c.t.TempDir(), "screen.ppm")
	c.monitorCmd("screendump " + ppm)
	var img *image.RGBA
	c.waitFor(30*time.Second, "the screen dump", func() bool {
		f, err := os.Open(ppm)
		if err != nil {
			return false
		}
		defer f.Close()
		img, err = readPPM(f)
		return err == nil
	})
	w, h := img.Bounds().Dx()/scale, img.Bounds().Dy()/scale
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.Set(x, y, img.At(x*scale, y*scale))
		}
	}
	return out
}

func readPPM(r io.Reader) (*image.RGBA, error) {
	br := bufio.NewReader(r)
	var magic string
	var w, h, max int
	if _, err := fmt.Fscan(br, &magic, &w, &h, &max); err != nil || magic != "P6" || max != 255 {
		return nil, fmt.Errorf("not a P6 ppm")
	}
	br.ReadByte()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	buf := make([]byte, 3*w)
	for y := 0; y < h; y++ {
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, err
		}
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{buf[3*x], buf[3*x+1], buf[3*x+2], 255})
		}
	}
	return img, nil
}

// golden compares a screen with testdata/<name>.png, writing it with -update-golden and
// leaving <name>.got.png beside it when they differ.
func (c *console) golden(name string, got *image.RGBA) {
	c.t.Helper()
	path := filepath.Join("testdata", name+".png")
	if *update {
		c.save(name, got)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		c.save(name+".got", got)
		c.t.Fatalf("%v (run with -update-golden once the screen is right)", err)
	}
	defer f.Close()
	want, err := png.Decode(f)
	if err != nil {
		c.t.Fatal(err)
	}
	if !same(got, want) {
		c.save(name+".got", got)
		c.t.Errorf("%s differs from testdata/%s.png (see testdata/%s.got.png)", name, name, name)
	}
}

func (c *console) save(name string, img image.Image) {
	c.t.Helper()
	f, err := os.Create(filepath.Join("testdata", name+".png"))
	if err != nil {
		c.t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		c.t.Fatal(err)
	}
}

func same(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
		for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
			ar, ag, ab, _ := a.At(x, y).RGBA()
			br, bg, bb, _ := b.At(x, y).RGBA()
			if ar != br || ag != bg || ab != bb {
				return false
			}
		}
	}
	return true
}

func tail(b []byte, n int) []byte {
	if len(b) > n {
		return b[len(b)-n:]
	}
	return bytes.TrimSpace(b)
}
