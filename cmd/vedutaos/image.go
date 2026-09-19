package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/riftbane/vedutaos/image"
	"github.com/riftbane/vedutaos/initramfs"
	"github.com/riftbane/vedutaos/panel"
)

// imageCommand builds the console image from the pinned packages. Linux or macOS with xz
// and mtools; CI does it for releases.
func imageCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("image", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "out", "`directory` for "+image.ImageName+", "+image.KernelName+" and "+image.InitrdName)
	cache := fs.String("cache", "", "`directory` keeping the downloaded packages (default: --out)")
	vshell := fs.String("vshell", "", "the console `program` (vshell) for linux/arm64 (default: built from source)")
	ver := fs.String("version", version, "version written to "+initramfs.Release)
	size := fs.Int64("size", image.DefaultSize>>20, "the image's size in `MiB`: one FAT32 volume, so what the card has beyond it is not used")
	rotate := fs.Int("rotate", 90, "how far the panel is turned to lie in landscape: 90 or 270")
	rgb := fs.Bool("rgb", false, "the panel's subpixels are red-green-blue (most are blue-green-red)")
	invert := fs.Bool("invert", false, "the panel shows colours inverted without it (common on IPS modules)")
	speed := fs.Int("spi-speed", image.DefaultSPISpeed, "SPI clock of the panel, in `hertz`")
	pins := fs.String("pins", "", "GPIO lines of the panel and the buttons, Raspberry Pi (BCM) numbers, as `dc=24,reset=25,backlight=18,up=5,down=6,left=13,right=19,a=26,b=21,select=20,cancel=16,home=12`; none for a line not connected")
	var games repeated
	fs.Var(&games, "game", "a game to put on the image's card: a game folder, a release `archive` or a Veduta project; repeat for more")
	if _, err := parse(fs, args); err != nil {
		return 2
	}
	if err := image.Check(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	p := image.DefaultPins
	if *pins != "" {
		var err error
		if p, err = parsePins(*pins, p); err != nil {
			fmt.Fprintln(stderr, "vedutaos image:", err)
			return 2
		}
	}
	vs, cleanup, err := findVShell(*vshell, *ver, stdout)
	if err != nil {
		fmt.Fprintln(stderr, "vedutaos image:", err)
		return 1
	}
	defer cleanup()
	r, err := image.Build(image.Options{
		VShell: vs, Version: *ver, Out: *out, Cache: *cache, Size: *size << 20,
		Panel:  panel.Options{Rotate: *rotate, RGB: *rgb, Invert: *invert},
		Wiring: image.Wiring{Pins: p, Speed: *speed}, Verbose: stdout, Games: games,
	})
	if err != nil {
		fmt.Fprintln(stderr, "vedutaos image:", err)
		return 1
	}
	fmt.Fprintf(stdout, "image: %s\nkernel for QEMU: %s\nconsole for QEMU: %s\nboot files for updates: %s\n", r.Image, r.Kernel, r.Initrd, r.Boot)
	return 0
}
