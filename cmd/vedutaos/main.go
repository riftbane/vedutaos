// Command vedutaos makes VedutaOS cards on a PC.
//
//	vedutaos card <dir> [flags]              write the console and games onto a card
//	vedutaos qemu [flags] [-- qemu-args]     make a card and boot the console in QEMU
//
// A card for a Raspberry Pi is the boot partition of a freshly written Raspberry Pi OS Lite
// (64-bit) card, as a PC sees it. A card for QEMU is a folder that QEMU shows the emulated
// machine as a disk. Either way the machine installs the console by itself the first time it
// starts; nothing is typed on it.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `vedutaos makes VedutaOS cards.

  vedutaos card <dir> [flags]            write the console and games onto a card
  vedutaos qemu [flags] [-- qemu-args]   make a card and boot the console in QEMU

Run "vedutaos card -h" or "vedutaos qemu -h" for the flags.
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
