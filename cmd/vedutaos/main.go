// Command vedutaos makes the VedutaOS image and puts games on cards, from a PC.
//
//	vedutaos image [flags]                   build the image (Linux, root)
//	vedutaos qemu [flags] [-- qemu-args]     boot the image in QEMU with a card of games
//	vedutaos card <dir> [flags]              put games, settings and keys onto a card
//	vedutaos flash <device> [flags]          write the image onto a card (Linux, macOS)
//
// The image is one file for a Raspberry Pi and for QEMU. A card is the boot partition of a
// console, a USB stick labelled VEDUTA, or the folder QEMU shows the emulated machine as a
// disk; games are folders on it, and nothing is typed on the console.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// version is set by the release build (-ldflags "-X main.version=v0.1.0").
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `vedutaos makes the VedutaOS image and its cards.

  vedutaos image [flags]                 build the image (Linux, root)
  vedutaos qemu [flags] [-- qemu-args]   boot the image in QEMU with a card of games
  vedutaos card <dir> [flags]            put games, settings and keys onto a card
  vedutaos flash <device> [flags]        write the image onto a card (Linux, macOS)
  vedutaos version                       print this program's version

Run "vedutaos <command> -h" for the flags.
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "card":
		return cardCommand(args[1:], stdout, stderr)
	case "qemu":
		return qemuCommand(args[1:], stdout, stderr)
	case "image":
		return imageCommand(args[1:], stdout, stderr)
	case "flash":
		return flashCommand(args[1:], stdout, stderr)
	case "version", "-version", "--version":
		fmt.Fprintln(stdout, "vedutaos", version)
		return 0
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "vedutaos: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// parse reads flags wherever they are among the arguments, so "card E:\ --game x" and
// "card --game x E:\" both work, and returns the rest.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return rest, nil
		}
		rest = append(rest, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// repeated is a flag that may be given more than once.
type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(s string) error { *r = append(*r, s); return nil }
