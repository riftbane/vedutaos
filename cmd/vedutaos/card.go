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

	"github.com/riftbane/veduta/v2/script"
	"github.com/riftbane/vedutaos/card"
	"github.com/riftbane/vedutaos/image"
	"github.com/riftbane/vedutaos/install"
)

// cardSettings are the flags that describe what goes onto a card, shared by the card and
// qemu commands. The console itself is in the image; a card carries games, settings, a
// request for shells and, to update a console from a PC, a dashboard.
type cardSettings struct {
	games   repeated
	scale   int
	fb, pad string
	debug   bool
	vshell  string
}

func (s *cardSettings) register(fs *flag.FlagSet) {
	fs.Var(&s.games, "game", "a game to put on the card: a game folder, a release `archive` (.tar.gz or .zip) or a Veduta project; repeat for more")
	fs.IntVar(&s.scale, "scale", 0, "VEDUTA_SCALE, `1-8`: how many screen pixels each panel pixel covers (default: the console's, 1; 4 in QEMU)")
	fs.StringVar(&s.fb, "fb", "", "VEDUTA_FB: the framebuffer to draw on, when the engine's choice is wrong")
	fs.StringVar(&s.pad, "pad", "", "VEDUTA_PAD: the one input device to read")
	fs.BoolVar(&s.debug, "debug", false, "ask the console for a shell on its serial port and on tty2 ("+card.DebugFile+")")
	fs.StringVar(&s.vshell, "vshell", "", "a dashboard `program` for linux/arm64 that replaces the image's while on the card")
}

func (s *cardSettings) validate() error {
	if s.scale < 0 || s.scale > 8 {
		return errors.New("--scale must be 1 to 8")
	}
	return nil
}

// env is the card's settings file, or nil when every setting is the console's own.
func (s *cardSettings) env() []byte {
	if s.scale == 0 && s.fb == "" && s.pad == "" {
		return nil
	}
	var b strings.Builder
	b.WriteString("# VedutaOS settings, read each time the dashboard starts; written by vedutaos card.\n")
	if s.scale != 0 {
		fmt.Fprintf(&b, "VEDUTA_SCALE=%d\n", s.scale)
	}
	if s.fb != "" {
		b.WriteString("VEDUTA_FB=" + s.fb + "\n")
	}
	if s.pad != "" {
		b.WriteString("VEDUTA_PAD=" + s.pad + "\n")
	}
	return []byte(b.String())
}

func cardCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vedutaos card", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var s cardSettings
	s.register(fs)
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: vedutaos card <dir> [flags]\n\n<dir> is the card as this PC sees it: a VedutaOS card (the drive named "+card.BootLabel+", E:\\ on\nWindows), a USB stick labelled "+card.Label+", or a folder for QEMU.\n\n")
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
	if err := s.validate(); err != nil {
		fmt.Fprintln(stderr, "vedutaos card:", err)
		return 2
	}
	if err := makeCard(rest[0], &s, stdout); err != nil {
		fmt.Fprintln(stderr, "vedutaos card:", err)
		return 1
	}
	if err := describe(stdout, rest[0]); err != nil {
		fmt.Fprintln(stderr, "vedutaos card:", err)
		return 1
	}
	return 0
}

// makeCard writes the games, settings, keys and dashboard the flags name onto the card in
// dir. What is not named is left as it is, so a card can be written again for one more game.
func makeCard(dir string, s *cardSettings, out io.Writer) error {
	if err := os.MkdirAll(filepath.Join(dir, card.Dir), 0o755); err != nil {
		return err
	}
	if s.vshell != "" {
		if arch, err := install.Arch(s.vshell); err != nil || arch != "arm64" {
			return fmt.Errorf("%s is not a linux/arm64 program (%s%v); the console cannot run it", s.vshell, arch, errOrEmpty(err))
		}
		if err := copyFile(s.vshell, filepath.Join(dir, filepath.FromSlash(card.VShell))); err != nil {
			return err
		}
	}
	if env := s.env(); env != nil {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(card.EnvFile)), env, 0o644); err != nil {
			return err
		}
	}
	if s.debug {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(card.DebugFile)), []byte("The console opens a shell on its serial port and on tty2 while this file is here.\n"), 0o644); err != nil {
			return err
		}
	}
	games := filepath.Join(dir, card.Games)
	if err := os.MkdirAll(games, 0o755); err != nil {
		return err
	}
	for _, g := range s.games {
		fmt.Fprintf(out, "adding %s\n", g)
		if _, err := install.Game(games, g, install.GoBuild); err != nil {
			return err
		}
	}
	return nil
}

func errOrEmpty(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

// describe says what the card now holds, listing the games as the console will list them.
func describe(out io.Writer, dir string) error {
	fmt.Fprintf(out, "\nCard in %s\n", dir)
	for _, f := range []struct{ name, what string }{{card.VShell, "a dashboard replacing the image's"}, {card.EnvFile, "settings"}, {card.DebugFile, "shells on the serial port and tty2"}, {card.ReleaseFile, "the image that wrote the card"}} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f.name))); err == nil {
			fmt.Fprintf(out, "  %-24s %s\n", f.name, f.what)
		}
	}
	cards, err := card.ScanFor(filepath.Join(dir, card.Games), "arm64")
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
		switch arch, err := install.Arch(c.Exec); {
		case c.Script != "" && c.API > script.APILevel:
			note = fmt.Sprintf("  ! needs Lua API level %d, this VedutaOS has %d", c.API, script.APILevel)
		case c.Script != "":
			note = "  Lua"
		case err != nil:
			note = "  ! " + err.Error()
		case arch != "arm64":
			note = "  ! built for " + arch + ", the console cannot run it"
		}
		if c.Problem != "" {
			note += "  (" + c.Problem + ")"
		}
		fmt.Fprintf(out, "    %-24s %s%s\n", c.Title, filepath.ToSlash(rel), note)
	}
	return nil
}

// parsePins reads "dc=24,reset=25,backlight=18"; a name left out keeps its value in pins,
// and "none" is a line not connected.
func parsePins(s string, pins image.Pins) (image.Pins, error) {
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
			if n < 0 {
				return pins, errors.New("--pins: dc must be connected")
			}
			pins.DC = n
		case "reset":
			pins.Reset = n
		case "backlight":
			pins.Backlight = n
		default:
			return pins, fmt.Errorf("--pins: unknown line %q (dc, reset, backlight)", name)
		}
	}
	return pins, nil
}

// buildVShell builds the console program from the source tree at root, stamped with the
// version.
var buildVShell = func(root, out, version string) error {
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X main.version="+version, "-o", out, "./cmd/vshell")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

// findVShell finds the console program for the image: the one named, the one shipped
// beside this program, or one built from the source tree this program runs in.
func findVShell(named, version string, out io.Writer) (string, func(), error) {
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
		return "", none, errors.New("no console program: pass --vshell, keep vshell-arm64 beside vedutaos, or run vedutaos from its source folder with Go installed")
	}
	tmp, err := os.MkdirTemp("", "vedutaos-vshell-")
	if err != nil {
		return "", none, err
	}
	cleanup := func() { os.RemoveAll(tmp) }
	p := filepath.Join(tmp, "vshell")
	fmt.Fprintf(out, "building the console (vshell %s) for linux/arm64 from %s\n", version, root)
	if err := buildVShell(root, p, version); err != nil {
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
