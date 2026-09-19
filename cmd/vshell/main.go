// Command vshell is the console: the dashboard that lists the games on the card, runs the
// one the player chooses, and comes back when that game ends.
//
// On the console it is also the operating system's first process: as PID 1 it mounts the
// kernel's file systems, loads the drivers, finds and mounts the card, and only then shows
// the dashboard (init_linux.go). There is nothing else running. On a desktop, or from a
// card that carries a newer copy of it, the same program is only the dashboard, in an
// ordinary window or on the console's panel.
//
// There is one panel and one pad, so the dashboard gives them up while a game is running:
// it closes its window before launching and opens it again afterwards.
//
// A game written in Lua is run by this same program, with the engine it is built with, in a
// process of its own (vshell play <folder>): a game that fails takes only itself down.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	veduta "github.com/riftbane/veduta/v2"
	"github.com/riftbane/veduta/v2/gfx"
	"github.com/riftbane/veduta/v2/gfx/soft"
	"github.com/riftbane/veduta/v2/platform"
	"github.com/riftbane/veduta/v2/script"
	"github.com/riftbane/veduta/v2/sim"
	"github.com/riftbane/veduta/v2/sprite"
	"github.com/riftbane/vedutaos/card"
	"github.com/riftbane/vedutaos/initramfs"
	"github.com/riftbane/vedutaos/shell"
	"github.com/riftbane/vedutaos/wifi"
)

// version is set by the image build (-ldflags "-X main.version=v0.3.0").
var version = "dev"

// The panel refreshes twenty times a second, and so does the dashboard.
const tickRate = 20

// rescan is how often the dashboard, while shown, reads the card again: a card can be
// mounted after the dashboard came up, and init keeps the text console off the panel.
const rescan = 2 * time.Second

// wifiRescan is how often the networks in range are looked for while the Wi-Fi screen is
// shown.
const wifiRescan = 10 * time.Second

// defaultGames is where a card's games are: the partition a PC sees when the card is
// plugged into it.
const defaultGames = initramfs.CardMount + "/" + card.Games

// Seams: tests drive the whole loop through a window that is not a window and a launcher
// that runs nothing; init hooks tick to keep the framebuffer console unbound.
var (
	openWindow = platform.Open
	launch     = runGame
	tick       = func() {}
)

// leaveOnClose is whether the player's close (Ctrl+Q, a window's close button, Home on a
// pad) leaves the dashboard. On the console, where the dashboard is init or init's child,
// it does not: Home there means the dashboard itself, and switching off is in the menu.
var leaveOnClose = !onConsole

// onConsole is whether this program is the console's dashboard: init, or init's child.
var onConsole = os.Getpid() == 1 || os.Getppid() == 1

// playCommand is the argument that makes this program play the script game in a folder.
const playCommand = "play"

func main() {
	if len(os.Args) == 3 && os.Args[1] == playCommand {
		os.Exit(script.Run([]string{"-project", os.Args[2]}, os.Stdout, os.Stderr))
	}
	if os.Getpid() == 1 {
		initMain() // never returns
	}
	openStatus()
	dir := flag.String("games", envOr("VEDUTAOS_GAMES", defaultGames), "directory holding the game folders")
	flag.Parse()
	if err := run(*dir); err != nil {
		fmt.Fprintln(os.Stderr, "vshell:", err)
		os.Exit(1)
	}
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// status is where the console says what it is doing: the kernel log, which reaches the
// serial port and dmesg, when it can be written; nowhere on a desktop.
var status io.Writer = io.Discard

func openStatus() {
	if f, err := os.OpenFile("/dev/kmsg", os.O_WRONLY, 0); err == nil {
		status = f
	}
}

// say writes one line of status. Each write is one record of the kernel log.
func say(format string, args ...any) {
	fmt.Fprintf(status, "<5>vedutaos: "+format+"\n", args...)
}

// children are the processes this program started and will wait for itself, so that
// init's reaper leaves them alone.
var children = struct {
	sync.Mutex
	pids map[int]bool
}{pids: map[int]bool{}}

func startChild(cmd *exec.Cmd) error {
	children.Lock()
	defer children.Unlock()
	if err := cmd.Start(); err != nil {
		return err
	}
	children.pids[cmd.Process.Pid] = true
	return nil
}

func waitChild(cmd *exec.Cmd) error {
	err := cmd.Wait()
	children.Lock()
	delete(children.pids, cmd.Process.Pid)
	children.Unlock()
	return err
}

func isChild(pid int) bool {
	children.Lock()
	defer children.Unlock()
	return children.pids[pid]
}

// network is the console's Wi-Fi: nil on a desktop, where the dashboard leaves the
// machine's network alone, and on an image with no wpa_supplicant. It is started once, and
// keeps running while games play and when the dashboard is started again.
var (
	network     *wifi.Manager
	networkOnce sync.Once
)

// startWiFi starts the Wi-Fi on the console. The networks it joins are remembered on the
// card when the card can be written.
func startWiFi() {
	if !onConsole || !fileOK(initramfs.WPASupplicant) {
		return
	}
	store := &wifi.Store{Path: filepath.Join(initramfs.CardMount, filepath.FromSlash(card.WiFiFile))}
	if os.Getenv(initramfs.SavesEnv) != "" {
		store.Write = filepath.Join(initramfs.CardWrite, filepath.FromSlash(card.WiFiFile))
	}
	network = wifi.Start(wifi.Options{
		SysNet:     "/sys/class/net",
		Supplicant: initramfs.WPASupplicant,
		Env:        []string{"LD_LIBRARY_PATH=" + initramfs.Libs},
		Busybox:    initramfs.Busybox,
		DHCPScript: initramfs.DHCPScript,
		RunDir:     "/tmp/wifi",
		Store:      store,
		Start:      startChild,
		Wait:       waitChild,
		Say:        say,
	})
}

func fileOK(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// run shows the dashboard until the player leaves it, launching games in between.
func run(dir string) error {
	networkOnce.Do(startWiFi)
	state := shell.State{Version: version}
	for {
		// The card is read again every time: a game may have been added or removed while
		// another was playing, and a folder can go missing under us.
		cards, err := card.Scan(dir)
		if err != nil {
			return err
		}
		state.Cards = cards
		next, action, err := show(dir, state)
		if err != nil {
			return err
		}
		state = next
		if action != shell.Launch {
			return nil // Quit, or the window was closed
		}
		if state.Sel >= len(state.Cards) {
			continue
		}
		if err := launch(state.Cards[state.Sel]); err != nil {
			state.Notice = notice(err)
		} else {
			state.Notice = ""
		}
	}
}

// show runs the dashboard until the player launches a game or leaves, with the window open
// only for as long as that takes.
func show(dir string, state shell.State) (shell.State, shell.Action, error) {
	tick()
	win, err := openWindow(platform.Options{Title: "VedutaOS", Width: 320, Height: 240})
	if err != nil {
		return state, shell.Quit, err
	}
	defer win.Close()
	say("dashboard: %s", plural(len(state.Cards), "game"))

	r := soft.New(soft.Options{})
	defer r.Close()
	font := sprite.DefaultFont()
	fontTex, err := r.CreateTexture(font.TextureData())
	if err != nil {
		return state, shell.Quit, err
	}
	res := shell.Resources{Font: font, FontTex: fontTex, Icons: loadIcons(r, state.Cards)}

	var (
		in       sim.InputState
		dl       gfx.DrawList
		fb       *gfx.Framebuffer
		period   = time.Second / tickRate
		next     = time.Now()
		nextScan = time.Now().Add(rescan)
		nextWiFi = time.Now().Add(wifiRescan)
	)
	for {
		if time.Now().After(nextScan) {
			nextScan = time.Now().Add(rescan)
			tick()
			if cards, err := card.Scan(dir); err == nil && !sameCards(cards, state.Cards) {
				state.Cards = cards
				res.Icons = loadIcons(r, cards)
			}
		}
		events, err := win.Poll()
		if err != nil {
			return state, shell.Quit, err
		}
		for _, e := range events {
			switch e.Kind {
			case platform.Press:
				in.Press(e.Button)
			case platform.Release:
				in.Release(e.Button)
			case platform.Close:
				if leaveOnClose {
					return state, shell.Quit, nil
				}
			case platform.FocusLost:
				in.ReleaseAll()
			}
		}
		state.WiFi = network.Status()
		if state.Screen == shell.WiFi && time.Now().After(nextWiFi) {
			network.Scan()
			nextWiFi = time.Now().Add(wifiRescan)
		}
		var action shell.Action
		was := state.Screen
		state, action = shell.Step(state, in.Next())
		if state.Screen != was {
			say("screen %s", state.Screen)
		}
		switch action {
		case shell.Stay:
		case shell.Scan:
			network.Scan()
			nextWiFi = time.Now().Add(wifiRescan)
		case shell.Join:
			network.Join(state.Target.SSID, state.Keys.Text)
			state.Keys = shell.Keyboard{} // the password is not kept
		default:
			return state, action, nil
		}
		w, h := win.Size()
		if w > 0 && h > 0 {
			if fb == nil || fb.W != w || fb.H != h {
				fb = gfx.NewFramebuffer(w, h, false)
			}
			dl.Reset()
			b := sprite.Begin(&dl, w, h)
			shell.Draw(b, res, w, h, state)
			b.End()
			if err := r.Begin(fb); err != nil {
				return state, shell.Quit, err
			}
			if err := r.Draw(&dl); err != nil {
				return state, shell.Quit, err
			}
			if err := r.End(); err != nil {
				return state, shell.Quit, err
			}
			if err := win.Present(fb.Image()); err != nil {
				return state, shell.Quit, err
			}
		}
		next = next.Add(period)
		if d := time.Until(next); d > 0 {
			time.Sleep(d)
		} else if -d > 5*period {
			next = time.Now() // far behind: skip ahead rather than race
		}
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// sameCards reports whether two scans of the card list the same games.
func sameCards(a, b []card.Card) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// loadIcons uploads the picture of every game that has one. A game without an icon, or
// with one that cannot be read, simply gets the placeholder tile.
func loadIcons(r *soft.Renderer, cards []card.Card) map[string]shell.Icon {
	icons := map[string]shell.Icon{}
	for _, c := range cards {
		if c.Icon == "" {
			continue
		}
		f, err := os.Open(c.Icon)
		if err != nil {
			continue
		}
		img, err := gfx.DecodePNG(f)
		f.Close()
		if err != nil || img.W <= 0 || img.H <= 0 {
			continue
		}
		tex, err := r.CreateTexture(&gfx.TextureData{Levels: gfx.BuildMips(img), Wrap: gfx.WrapClamp})
		if err != nil {
			continue
		}
		icons[c.Dir] = shell.Icon{Tex: tex, W: img.W, H: img.H}
	}
	return icons
}

// runGame runs a game and waits for it: a script game through this program, a program
// itself. The card was written by a PC, where a file has no permission to execute, so the
// bit is set here rather than asked of the player (on the console the card is FAT, where
// every file is executable already).
func runGame(c card.Card) error {
	cmd, err := gameCommand(c)
	if err != nil {
		say("game %s: %v", c.Title, err)
		return err
	}
	cmd.Dir = c.Dir
	cmd.Env = gameEnv(os.Environ(), os.Getenv(initramfs.SavesEnv), c)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := startChild(cmd); err != nil {
		say("game %s: %v", c.Title, err)
		return err
	}
	say("game %s started", c.Title)
	err = waitChild(cmd)
	if err != nil {
		say("game %s ended: %v", c.Title, err)
	} else {
		say("game %s ended", c.Title)
	}
	return err
}

// gameEnv is a game's environment: the dashboard's, and when the card takes saves (saves is
// its saves folder), the game's own folder of saves in it, named after the game's folder.
func gameEnv(env []string, saves string, c card.Card) []string {
	if saves == "" {
		return env
	}
	return append(env, veduta.SaveDirEnv+"="+filepath.Join(saves, filepath.Base(c.Dir)))
}

// gameCommand is the process that plays a game.
func gameCommand(c card.Card) (*exec.Cmd, error) {
	if c.Script == "" {
		if fi, err := os.Stat(c.Exec); err == nil && fi.Mode().Perm()&0o111 == 0 {
			os.Chmod(c.Exec, fi.Mode().Perm()|0o755)
		}
		return exec.Command(c.Exec), nil
	}
	if c.API > script.APILevel {
		return nil, fmt.Errorf("the game needs Lua API level %d and this console has %d: update VedutaOS", c.API, script.APILevel)
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return exec.Command(self, playCommand, c.Dir), nil
}

// notice turns a failure into the single line the dashboard has room for.
func notice(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s)-i < 40 {
		s = s[i+2:]
	}
	return strings.ToUpper(filepath.Base(s))
}
