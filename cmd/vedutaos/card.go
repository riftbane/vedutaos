package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/riftbane/vedutaos/card"
	"github.com/riftbane/vedutaos/install"
	"github.com/riftbane/vedutaos/panel"
	"github.com/riftbane/vedutaos/provision"
)

// cardSettings are the flags that describe a card, shared by the card and qemu commands.
type cardSettings struct {
	games           repeated
	panel           string
	rotate          int
	rgb, invert     bool
	spiSpeed        int
	pins            string
	scale           int
	fb, pad         string
	sshKeys         repeated
	vshell          string
	replaceUserData bool
}

func (s *cardSettings) register(fs *flag.FlagSet, target provision.Target) {
	fs.Var(&s.games, "game", "a game to put on the card: a game folder, a release `archive` (.tar.gz or .zip) or a Veduta project; repeat for more")
	if target == provision.Pi {
		fs.StringVar(&s.panel, "panel", "ili9341", "the panel wired to SPI0: ili9341, or none to use HDMI")
		fs.IntVar(&s.rotate, "rotate", 90, "how far the panel is turned to lie in landscape: 90 or 270")
		fs.BoolVar(&s.rgb, "rgb", false, "the panel's subpixels are red-green-blue (most are blue-green-red)")
		fs.BoolVar(&s.invert, "invert", false, "the panel shows colours inverted without it (common on IPS modules)")
		fs.IntVar(&s.spiSpeed, "spi-speed", provision.DefaultSPISpeed, "SPI clock of the panel, in `hertz`")
		fs.StringVar(&s.pins, "pins", "", "GPIO lines of the panel, BCM numbers, as `dc=24,reset=25,backlight=18`; none for a line not connected")
		fs.BoolVar(&s.replaceUserData, "replace-user-data", false, "overwrite a user-data file that another tool wrote (Raspberry Pi Imager's customisation)")
	}
	fs.IntVar(&s.scale, "scale", 0, "VEDUTA_SCALE, `1-8` (default 1 on a Pi, 4 in QEMU)")
	fs.StringVar(&s.fb, "fb", "", "VEDUTA_FB: the framebuffer to draw on, when the engine's choice is wrong")
	fs.StringVar(&s.pad, "pad", "", "VEDUTA_PAD: the one input device to read")
	fs.Var(&s.sshKeys, "ssh-key", "a public key `file` whose keys may log in as veduta over ssh; repeat for more")
	fs.StringVar(&s.vshell, "vshell", "", "the dashboard `program` for linux/arm64 (default: vshell-arm64 beside this program, or built from source)")
}

// options turns the flags into what the card is made from.
func (s *cardSettings) options(target provision.Target) (provision.Options, panel.Options, error) {
	o := provision.Options{Target: target, Scale: s.scale, FB: s.fb, Pad: s.pad, Pins: provision.DefaultPins, SPISpeed: s.spiSpeed}
	if o.Scale == 0 {
		o.Scale = 1
		if target == provision.QEMU {
			o.Scale = 4
		}
	}
	switch s.panel {
	case "", "none":
	case "ili9341":
		o.Panel = target == provision.Pi
	default:
		return o, panel.Options{}, fmt.Errorf("unknown panel %q (want ili9341 or none)", s.panel)
	}
	if s.pins != "" {
		pins, err := parsePins(s.pins, o.Pins)
		if err != nil {
			return o, panel.Options{}, err
		}
		o.Pins = pins
	}
	for _, f := range s.sshKeys {
		data, err := os.ReadFile(f)
		if err != nil {
			return o, panel.Options{}, err
		}
		for _, l := range strings.Split(string(data), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				o.SSHKeys = append(o.SSHKeys, l)
			}
		}
	}
	return o, panel.Options{Rotate: s.rotate, RGB: s.rgb, Invert: s.invert}, o.Validate()
}

func parsePins(s string, pins provision.Pins) (provision.Pins, error) {
	for _, part := range strings.Split(s, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		n := -1
		if value != "none" {
			v, err := strconv.Atoi(value)
			if err != nil || v < 0 {
				ok = false
			}
			n = v
		}
		if !ok {
			return pins, fmt.Errorf("--pins: %q is not name=GPIO number", part)
		}
		switch name {
		case "dc":
			pins.DC = n
		case "reset":
			pins.Reset = n
		case "backlight":
			pins.Backlight = n
		default:
			return pins, fmt.Errorf("--pins: unknown line %q (want dc, reset, backlight)", name)
		}
	}
	return pins, nil
}

func cardCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vedutaos card", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var s cardSettings
	target := string(provision.Pi)
	fs.StringVar(&target, "target", target, "what the card is for: pi (a Raspberry Pi OS boot partition) or qemu (a folder)")
	s.register(fs, provision.Pi)
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: vedutaos card <dir> [flags]\n\n<dir> is the boot partition of a Raspberry Pi OS Lite (64-bit) card, as this PC\nsees it (E:\\ on Windows), or with -target qemu any folder.\n\n")
		fs.PrintDefaults()
	}
	rest, err := parse(fs, args)
	if err != nil {
		return 2
	}
	if len(rest) != 1 {
		fs.Usage()
		return 2
	}
	o, p, err := s.options(provision.Target(target))
	if err != nil {
		fmt.Fprintln(stderr, "vedutaos card:", err)
		return 2
	}
	if err := makeCard(rest[0], o, p, &s, stdout); err != nil {
		fmt.Fprintln(stderr, "vedutaos card:", err)
		return 1
	}
	if o.Target == provision.Pi {
		fmt.Fprintln(stdout, "\nPut the card in the Raspberry Pi and switch it on. The first start installs the console\nand restarts once; then the dashboard appears. Later, games are folders dropped into games\\.")
	}
	return 0
}

// makeCard writes the console and the games onto the card in dir.
func makeCard(dir string, o provision.Options, p panel.Options, s *cardSettings, out io.Writer) error {
	if o.Target == provision.Pi {
		for _, f := range []string{provision.ConfigFile, provision.CmdlineFile} {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				return fmt.Errorf("%s has no %s: it is not the boot partition of a Raspberry Pi OS card", dir, f)
			}
		}
		if ud, err := os.ReadFile(filepath.Join(dir, provision.UserDataFile)); err == nil && !provision.Replaceable(ud) && !s.replaceUserData {
			return fmt.Errorf("%s holds a user-data file written by another tool (Raspberry Pi Imager's customisation?). Write the card again without customisation, or pass --replace-user-data", dir)
		}
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, provision.Dir), 0o755); err != nil {
		return err
	}

	vshell, cleanup, err := findVShell(s.vshell, out)
	if err != nil {
		return err
	}
	defer cleanup()
	if arch, err := install.Arch(vshell); err != nil || arch != "arm64" {
		return fmt.Errorf("%s is not a linux/arm64 program (%s%v); the console cannot run it", vshell, arch, errOrEmpty(err))
	}
	if err := copyFile(vshell, filepath.Join(dir, filepath.FromSlash(provision.VShell))); err != nil {
		return err
	}

	var firmware []byte
	fwPath := filepath.Join(dir, filepath.FromSlash(provision.Firmware))
	if o.Panel {
		cmds, err := panel.ILI9341(p)
		if err != nil {
			return err
		}
		if firmware, err = panel.Encode(cmds); err != nil {
			return err
		}
		if err := os.WriteFile(fwPath, firmware, 0o644); err != nil {
			return err
		}
	} else if err := os.Remove(fwPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	userData, err := provision.UserData(o)
	if err != nil {
		return err
	}
	files := map[string][]byte{
		provision.EnvFile:      provision.Env(o),
		provision.UserDataFile: userData,
		provision.MetaDataFile: provision.MetaData(userData, firmware),
	}
	if o.Target == provision.Pi {
		files[provision.UserConfFile] = provision.UserConf()
		for _, name := range []string{provision.ConfigFile, provision.CmdlineFile} {
			old, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return err
			}
			if name == provision.ConfigFile {
				files[name] = provision.ConfigTxt(old, o)
			} else if files[name], err = provision.Cmdline(old); err != nil {
				return err
			}
		}
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), data, 0o644); err != nil {
			return err
		}
	}

	games := filepath.Join(dir, provision.Games)
	if err := os.MkdirAll(games, 0o755); err != nil {
		return err
	}
	for _, g := range s.games {
		fmt.Fprintf(out, "adding %s\n", g)
		if _, err := install.Game(games, g, install.GoBuild); err != nil {
			return err
		}
	}
	return describe(out, dir, o)
}

func errOrEmpty(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

// describe says what the card now holds, listing the games as the console will list them.
func describe(out io.Writer, dir string, o provision.Options) error {
	what := "QEMU"
	if o.Target == provision.Pi {
		what = "a Raspberry Pi on HDMI"
		if o.Panel {
			what = "a Raspberry Pi with an ILI9341 panel"
		}
	}
	fmt.Fprintf(out, "\nCard for %s in %s\n", what, dir)
	cards, err := card.ScanFor(filepath.Join(dir, provision.Games), "arm64")
	if err != nil {
		return err
	}
	if len(cards) == 0 {
		fmt.Fprintln(out, "  no games yet: add one with --game, or drop a game folder into games")
		return nil
	}
	fmt.Fprintf(out, "  %d game(s), as the dashboard will list them:\n", len(cards))
	for _, c := range cards {
		rel, _ := filepath.Rel(dir, c.Dir)
		note := ""
		if arch, err := install.Arch(c.Exec); err != nil {
			note = "  ! " + err.Error()
		} else if arch != "arm64" {
			note = "  ! built for " + arch + ", the console cannot run it"
		}
		if c.Problem != "" {
			note += "  (" + c.Problem + ")"
		}
		fmt.Fprintf(out, "    %-24s %s%s\n", c.Title, filepath.ToSlash(rel), note)
	}
	return nil
}

// buildVShell builds the dashboard for the console from the source tree at root.
var buildVShell = func(root, out string) error {
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", out, "./cmd/vshell")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

// findVShell finds the dashboard program for the console: the one named, the one shipped
// beside this program, or one built from the source tree this program runs in.
func findVShell(named string, out io.Writer) (string, func(), error) {
	none := func() {}
	if named != "" {
		return named, none, nil
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "vshell-arm64")
		if _, err := os.Stat(p); err == nil {
			return p, none, nil
		}
	}
	root, ok := sourceRoot()
	if !ok {
		return "", none, errors.New("no dashboard program for the console: pass --vshell, keep vshell-arm64 beside vedutaos, or run vedutaos from its source folder with Go installed")
	}
	tmp, err := os.MkdirTemp("", "vedutaos-vshell-")
	if err != nil {
		return "", none, err
	}
	cleanup := func() { os.RemoveAll(tmp) }
	p := filepath.Join(tmp, "vshell")
	fmt.Fprintf(out, "building the dashboard for linux/arm64 from %s\n", root)
	if err := buildVShell(root, p); err != nil {
		cleanup()
		return "", none, fmt.Errorf("building vshell: %w", err)
	}
	return p, cleanup, nil
}

// sourceRoot finds the vedutaos source tree containing the working directory.
func sourceRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			first, _, _ := strings.Cut(string(data), "\n")
			return dir, strings.TrimSpace(first) == "module github.com/riftbane/vedutaos"
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, in); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
