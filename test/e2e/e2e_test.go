//go:build linux

// Package e2e boots the built image on QEMU and drives the console as a person would: the
// dashboard comes up, lists the card's game, starts it on A, and comes back on Home. It is
// the test of the image itself, so it runs only when asked:
//
//	VEDUTAOS_E2E=1 go test ./test/e2e -timeout 40m
//
// It needs out/vedutaos.img with vmlinuz and initrd.img beside it (vedutaos image), QEMU,
// ssh and ssh-keygen. Under emulation the boot alone takes minutes.
package e2e

import (
	"bufio"
	"bytes"
	"errors"
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
	"strconv"
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
	ssh     []string
	log     *os.File
}

func TestConsole(t *testing.T) {
	if os.Getenv("VEDUTAOS_E2E") == "" {
		t.Skip("set VEDUTAOS_E2E=1 to boot the image on QEMU")
	}
	for _, tool := range []string{"qemu-system-aarch64", "qemu-img", "ssh", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is not installed", tool)
		}
	}
	img := os.Getenv("VEDUTAOS_IMAGE")
	if img == "" {
		img = filepath.Join("..", "..", "out", "vedutaos.img")
	}
	if img2, err := filepath.Abs(img); err == nil {
		img = img2
	}
	if _, err := os.Stat(img); err != nil {
		t.Fatalf("no image at %s: build one with vedutaos image, or set VEDUTAOS_IMAGE", img)
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
	key := filepath.Join(work, "key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	port := freePort(t)
	monitor := filepath.Join(work, "monitor.sock")
	logf, err := os.Create(filepath.Join(work, "qemu.log"))
	if err != nil {
		t.Fatal(err)
	}
	c := &console{t: t, monitor: monitor, log: logf}
	c.ssh = []string{"ssh", "-i", key, "-p", strconv.Itoa(port), "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR", "-o", "ConnectTimeout=3", "-o", "BatchMode=yes", "veduta@127.0.0.1"}

	// The tool makes the card and boots the image as it would for a person, with the screen
	// and the monitor moved where the test can reach them.
	c.qemu = exec.Command(tool, "qemu", "--image", img, "--card", filepath.Join(work, "card"), "--game", demo,
		"--ssh-key", key+".pub", "--ssh-port", strconv.Itoa(port), "--fresh",
		"--", "-display", "none", "-monitor", "unix:"+monitor+",server,nowait")
	c.qemu.Env = append(os.Environ(), "XDG_CACHE_HOME="+filepath.Join(work, "cache"), "HOME="+work)
	c.qemu.Stdout, c.qemu.Stderr = logf, logf
	// The tool and QEMU share a process group, so stopping the test stops the machine.
	c.qemu.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.qemu.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		syscall.Kill(-c.qemu.Process.Pid, syscall.SIGKILL)
		c.qemu.Wait()
		logf.Close()
		if t.Failed() {
			b, _ := os.ReadFile(logf.Name())
			t.Logf("QEMU and serial output:\n%s", tail(b, 20000))
		}
	})

	t.Log("waiting for the console to boot")
	c.waitFor(25*time.Minute, "ssh", func() bool { return c.run("true") == nil })
	if out, err := c.output("systemctl is-active vedutaos vedutaos-card"); err != nil || out != "active\nactive\n" {
		t.Fatalf("units: %q %v", out, err)
	}
	if out, _ := c.output("findmnt -no SOURCE /boot/firmware"); !strings.HasSuffix(strings.TrimSpace(out), "vdb1") {
		t.Errorf("the card is not mounted on /boot/firmware: %q", out)
	}
	if out, _ := c.output("ls /boot/firmware/games/demo"); !strings.Contains(out, "card.json") {
		t.Errorf("the game is not on the card: %q", out)
	}

	// The dashboard lists the game.
	time.Sleep(3 * time.Second)
	list := c.screen()
	c.golden("list", list)

	// A starts it; the game draws something else. A key is pressed again while waiting: the
	// emulated keyboard drops one now and then.
	c.press("spc", 2*time.Minute, "the game to start", func() bool { r, err := c.running(); return err == nil && r })
	time.Sleep(5 * time.Second)
	if game := c.screen(); same(game, list) {
		c.save("game.got", game)
		t.Error("the screen did not change when the game started")
	}

	// Home ends it; the dashboard is back as it was.
	c.press("ctrl-q", 2*time.Minute, "the game to end", func() bool { r, err := c.running(); return err == nil && !r })
	time.Sleep(3 * time.Second)
	c.golden("list", c.screen())
	if out, err := c.output("systemctl is-active vedutaos"); err != nil || out != "active\n" {
		t.Errorf("the dashboard is not running after the game: %q %v", out, err)
	}
}

// press presses a key, and again every fifteen seconds, until ok.
func (c *console) press(key string, d time.Duration, what string, ok func() bool) {
	c.t.Helper()
	last := time.Now()
	c.key(key)
	c.waitFor(d, what, func() bool {
		if ok() {
			return true
		}
		if time.Since(last) > 15*time.Second {
			c.key(key)
			last = time.Now()
		}
		return false
	})
}

func (c *console) waitFor(d time.Duration, what string, ok func() bool) {
	c.t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			c.t.Fatalf("waited %v for %s", d, what)
		}
		if c.qemu.ProcessState != nil {
			c.t.Fatalf("QEMU exited while waiting for %s", what)
		}
		time.Sleep(5 * time.Second)
	}
}

func (c *console) run(cmd string) error {
	_, err := c.output(cmd)
	return err
}

// output runs a command on the console over ssh. A connection that fails (exit 255) is
// tried again: the emulated machine drops one now and then.
func (c *console) output(cmd string) (string, error) {
	var out []byte
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		out, err = exec.Command(c.ssh[0], append(c.ssh[1:], cmd)...).Output()
		var exit *exec.ExitError
		if err == nil || !errors.As(err, &exit) || exit.ExitCode() != 255 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	return string(out), err
}

// running reports whether the game is running on the console; it waits for ssh to answer.
func (c *console) running() (bool, error) {
	out, err := c.output("pgrep -x demo >/dev/null; echo $?")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "0", nil
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

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func tail(b []byte, n int) []byte {
	if len(b) > n {
		return b[len(b)-n:]
	}
	return bytes.TrimSpace(b)
}
