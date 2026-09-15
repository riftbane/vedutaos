package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// flashCommand writes the image onto a card. Linux and macOS write the device
// themselves; on Windows, Raspberry Pi Imager writes the same .img.xz.
func flashCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vedutaos flash", flag.ContinueOnError)
	fs.SetOutput(stderr)
	img := fs.String("image", "", "the `image` to write, .img or .img.xz (default: this release's, fetched once)")
	yes := fs.Bool("yes", false, "write without asking")
	fs.Usage = func() {
		fmt.Fprint(stderr, "usage: vedutaos flash <device> [flags]\n\n<device> is the whole card, not a partition: /dev/sdX or /dev/mmcblk0 on Linux,\n/dev/rdiskN on macOS (diskutil list). Everything on it is lost. On Windows, write the\nimage with Raspberry Pi Imager (Use custom).\n\n")
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
	fail := func(err error) int {
		fmt.Fprintln(stderr, "vedutaos flash:", err)
		return 1
	}
	dev := rest[0]
	if err := checkDevice(dev); err != nil {
		return fail(err)
	}
	src := *img
	if src == "" {
		if src, err = fetchRelease(version, stdout); err != nil {
			return fail(err)
		}
	}
	if !fileOK(src) {
		return fail(fmt.Errorf("%s is missing", src))
	}
	if !*yes {
		fmt.Fprintf(stdout, "Write %s onto %s, losing everything on it? [y/N] ", src, dev)
		var answer string
		fmt.Fscanln(os.Stdin, &answer)
		if !strings.EqualFold(answer, "y") {
			fmt.Fprintln(stdout, "nothing written")
			return 1
		}
	}
	if err := writeImage(src, dev, stdout); err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "\nDone. Put the card in a Raspberry Pi: the dashboard appears on the panel. Games go onto\nthe card's boot partition with \"vedutaos card <its folder> --game …\".\n")
	return 0
}

// checkDevice refuses what is not a whole block device, or is in use.
func checkDevice(dev string) error {
	switch goos {
	case "windows":
		return errors.New("on Windows, write the image with Raspberry Pi Imager: Use custom, then this release's vedutaos.img.xz")
	case "linux", "darwin":
	default:
		return fmt.Errorf("writing a card is not supported on %s", goos)
	}
	fi, err := os.Stat(dev)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeDevice == 0 {
		return fmt.Errorf("%s is not a device", dev)
	}
	if goos == "linux" {
		base := filepath.Base(dev)
		if _, err := os.Stat("/sys/block/" + base); err != nil {
			return fmt.Errorf("%s is a partition or not a disk: name the whole card (/dev/sdX, /dev/mmcblk0)", dev)
		}
		if mounts, err := os.ReadFile("/proc/mounts"); err == nil && strings.Contains(string(mounts), dev) {
			return fmt.Errorf("%s has a mounted partition: unmount it first", dev)
		}
		if os.Geteuid() != 0 {
			return errors.New("writing a card needs root: run with sudo")
		}
	}
	return nil
}

// writeImage streams src, unpacking .xz on the way, onto dev and waits for it to reach
// the card.
func writeImage(src, dev string, out io.Writer) error {
	var r io.Reader
	var size int64
	if strings.HasSuffix(src, ".xz") {
		xz, err := lookPath("xz")
		if err != nil {
			return errors.New("xz is not installed, and the image is an .xz")
		}
		cmd := exec.Command(xz, "-dc", src)
		cmd.Stderr = os.Stderr
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		defer cmd.Wait()
		r = pipe
	} else {
		f, err := os.Open(src)
		if err != nil {
			return err
		}
		defer f.Close()
		if fi, err := f.Stat(); err == nil {
			size = fi.Size()
		}
		r = f
	}
	d, err := os.OpenFile(dev, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	p := &progress{w: out, total: size}
	_, err = io.Copy(io.MultiWriter(d, p), r)
	p.done()
	if err != nil {
		d.Close()
		return fmt.Errorf("writing %s: %w", dev, err)
	}
	fmt.Fprintln(out, "waiting for the card to take it all")
	if err := d.Sync(); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}
