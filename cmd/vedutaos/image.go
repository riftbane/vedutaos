package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/riftbane/vedutaos/image"
	"github.com/riftbane/vedutaos/panel"
)

// imageCommand builds the console image. Linux and root only; CI does it for releases.
func imageCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("image", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "out", "`directory` for vedutaos.img, vmlinuz and initrd.img")
	cache := fs.String("cache", "", "`directory` keeping the downloaded base image (default: --out)")
	vshell := fs.String("vshell", "", "the dashboard `program` for linux/arm64 (default: built from source)")
	ver := fs.String("version", version, "version written to "+image.Release)
	rotate := fs.Int("rotate", 90, "how far the panel is turned to lie in landscape: 90 or 270")
	rgb := fs.Bool("rgb", false, "the panel's subpixels are red-green-blue (most are blue-green-red)")
	invert := fs.Bool("invert", false, "the panel shows colours inverted without it (common on IPS modules)")
	speed := fs.Int("spi-speed", image.DefaultSPISpeed, "SPI clock of the panel, in `hertz`")
	pins := fs.String("pins", "", "GPIO lines of the panel, BCM numbers, as `dc=24,reset=25,backlight=18`; none for a line not connected")
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
	vs, cleanup, err := findVShell(*vshell, stdout)
	if err != nil {
		fmt.Fprintln(stderr, "vedutaos image:", err)
		return 1
	}
	defer cleanup()
	r, err := image.Build(image.Options{
		VShell: vs, Version: *ver, Out: *out, Cache: *cache,
		Panel:  panel.Options{Rotate: *rotate, RGB: *rgb, Invert: *invert},
		Wiring: image.Wiring{Pins: p, Speed: *speed}, Verbose: stdout,
	})
	if err != nil {
		fmt.Fprintln(stderr, "vedutaos image:", err)
		return 1
	}
	fmt.Fprintf(stdout, "image: %s\nkernel for QEMU: %s\ninitrd for QEMU: %s\n", r.Image, r.Kernel, r.Initrd)
	return 0
}
