package image

import (
	"errors"
	"strconv"
	"strings"

	"github.com/riftbane/vedutaos/panel"
)

// Pins are the Raspberry Pi GPIO lines (BCM numbering) the panel is wired to. A negative
// Reset or Backlight means the line is not connected.
type Pins struct {
	DC, Reset, Backlight int
}

// DefaultPins is the wiring the documentation shows.
var DefaultPins = Pins{DC: 24, Reset: 25, Backlight: 18}

// DefaultSPISpeed is the SPI clock of the panel, in hertz.
const DefaultSPISpeed = 32000000

// Wiring says how the panel is connected.
type Wiring struct {
	Pins  Pins
	Speed int // SPI hertz
}

const (
	blockBegin = "# vedutaos begin: the console's panel."
	blockEnd   = "# vedutaos end"
)

// ConfigTxt returns a Raspberry Pi config.txt with the console's block in it: the panel's
// overlay for w, nothing when w is nil (HDMI). Everything else in the file is kept as it
// was, so running it again, or with other settings, changes only the block.
func ConfigTxt(existing []byte, w *Wiring) []byte {
	lines := strings.SplitAfter(string(existing), "\n")
	var kept []string
	inside := false
	for _, l := range lines {
		t := strings.TrimRight(l, "\r\n")
		switch {
		case !inside && strings.HasPrefix(t, "# vedutaos begin"):
			inside = true
		case inside && t == blockEnd:
			inside = false
		case !inside && l != "":
			kept = append(kept, l)
		}
	}
	out := strings.Join(kept, "")
	if w == nil {
		return []byte(out)
	}
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	gpios := "dc-gpio=" + strconv.Itoa(w.Pins.DC)
	if w.Pins.Reset >= 0 {
		gpios = "reset-gpio=" + strconv.Itoa(w.Pins.Reset) + "," + gpios
	}
	if w.Pins.Backlight >= 0 {
		gpios += ",backlight-gpio=" + strconv.Itoa(w.Pins.Backlight)
	}
	return []byte(out + blockBegin + "\n" +
		"[all]\n" +
		"dtparam=spi=on\n" +
		"dtoverlay=mipi-dbi-spi,spi0-0,speed=" + strconv.Itoa(w.Speed) + ",write-only\n" +
		"dtparam=compatible=" + panel.Compatible + `\0panel-mipi-dbi-spi` + "\n" +
		"dtparam=width=320,height=240\n" +
		"dtparam=" + gpios + "\n" +
		blockEnd + "\n")
}

// CmdlineSettings are the kernel settings a console needs: no blinking cursor on the text
// console, and no blanking it after ten minutes without a key. QEMU gets them on its own
// command line; the Pi reads them from cmdline.txt.
var CmdlineSettings = []string{"vt.global_cursor_default=0", "consoleblank=0"}

// Cmdline returns a Raspberry Pi cmdline.txt with the console's kernel settings, replacing
// any earlier value of the same settings and keeping everything else.
func Cmdline(existing []byte) ([]byte, error) {
	text := strings.TrimRight(string(existing), "\r\n")
	if text == "" || strings.ContainsAny(text, "\r\n") {
		return nil, errors.New("image: cmdline.txt must hold exactly one line")
	}
	var out []string
	for _, f := range strings.Fields(text) {
		key, _, _ := strings.Cut(f, "=")
		drop := false
		for _, s := range CmdlineSettings {
			if k, _, _ := strings.Cut(s, "="); k == key {
				drop = true
			}
		}
		if !drop {
			out = append(out, f)
		}
	}
	out = append(out, CmdlineSettings...)
	return []byte(strings.Join(out, " ") + "\n"), nil
}
