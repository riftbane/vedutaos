package image

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/riftbane/vedutaos/panel"
)

// Pins are the GPIO lines of the 40-pin header, by their Raspberry Pi numbers (BCM), that
// the panel and the handheld's buttons are wired to. The Orange Pi Zero 2W has the same
// header, so the same numbers name the same pins there. A negative line is not connected;
// DC must be. Each button is wired between its line and ground.
type Pins struct {
	DC, Reset, Backlight                              int
	Up, Down, Left, Right, A, B, Select, Cancel, Home int
}

// DefaultPins is the wiring the documentation shows.
var DefaultPins = Pins{DC: 24, Reset: 25, Backlight: 18,
	Up: 5, Down: 6, Left: 13, Right: 19, A: 26, B: 21, Select: 20, Cancel: 16, Home: 12}

// Line is one wired line: its name in --pins, the GPIO number, and for a button the key
// code it reports, the one the engine reads as that button (0 for the panel's lines).
type Line struct {
	Name string
	GPIO *int
	Code uint32
}

// Key codes of linux/input-event-codes.h the buttons report.
const (
	btnDPadUp = 0x220 // BTN_DPAD_UP, then DOWN, LEFT, RIGHT
	btnSouth  = 0x130 // A
	btnEast   = 0x131 // B
	btnSelect = 0x13a
	btnMode   = 0x13c // Home
	keyBack   = 158   // Cancel
)

// Lines lists every line of p, the panel's first, so that they can be set by name.
func (p *Pins) Lines() []Line {
	return []Line{
		{"dc", &p.DC, 0}, {"reset", &p.Reset, 0}, {"backlight", &p.Backlight, 0},
		{"up", &p.Up, btnDPadUp}, {"down", &p.Down, btnDPadUp + 1}, {"left", &p.Left, btnDPadUp + 2}, {"right", &p.Right, btnDPadUp + 3},
		{"a", &p.A, btnSouth}, {"b", &p.B, btnEast}, {"select", &p.Select, btnSelect}, {"cancel", &p.Cancel, keyBack}, {"home", &p.Home, btnMode},
	}
}

// Check refuses wiring the header cannot have: a line that is not a GPIO of the header,
// one used twice, one of the SPI bus or of the serial port, or DC not connected.
func (p Pins) Check() error {
	if p.DC < 0 {
		return errors.New("pins: dc must be connected")
	}
	used := map[int]string{8: "SPI CE0", 9: "SPI MISO", 10: "SPI MOSI", 11: "SPI SCLK", 14: "the serial port", 15: "the serial port"}
	for _, l := range p.Lines() {
		n := *l.GPIO
		if n < 0 {
			continue
		}
		if n > 27 {
			return fmt.Errorf("pins: %s=%d is not a GPIO of the 40-pin header (0 to 27)", l.Name, n)
		}
		if other, ok := used[n]; ok {
			return fmt.Errorf("pins: %s=%d is already %s", l.Name, n, other)
		}
		used[n] = l.Name
	}
	return nil
}

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

// stockConfig is the config.txt the image starts from: what Raspberry Pi OS sets that a
// console needs, and nothing for HDMI, cameras or audio. The kernels and initramfs files
// are found by their standard names (kernel8.img and initramfs8, kernel_2712.img and
// initramfs_2712).
const stockConfig = `# VedutaOS. The console's panel is in the block at the end; see
# https://github.com/riftbane/vedutaos/blob/main/docs/quickstart-pi.md
arm_64bit=1
auto_initramfs=1
disable_fw_kms_setup=1
disable_overscan=1
arm_boost=1

[cm4]
otg_mode=1

[cm5]
dtoverlay=dwc2,dr_mode=host

[pi5]
dtoverlay=nospi10

[all]
`

// ConfigTxt returns a Raspberry Pi config.txt with the console's block in it: the panel's
// overlay for w, nothing when w is nil (HDMI). Everything else in the file is kept as it
// was, so running it again, or with other settings, changes only the block; an empty
// existing file starts from the image's own.
func ConfigTxt(existing []byte, w *Wiring) []byte {
	if len(existing) == 0 {
		existing = []byte(stockConfig)
	}
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
	var keys strings.Builder
	for _, l := range w.Pins.Lines() {
		if l.Code != 0 && *l.GPIO >= 0 {
			fmt.Fprintf(&keys, "dtoverlay=gpio-key,gpio=%d,keycode=%d,label=%s\n", *l.GPIO, l.Code, l.Name)
		}
	}
	return []byte(out + blockBegin + "\n" +
		"[all]\n" +
		"dtparam=spi=on\n" +
		"dtoverlay=mipi-dbi-spi,spi0-0,speed=" + strconv.Itoa(w.Speed) + ",write-only\n" +
		"dtparam=compatible=" + panel.Compatible + `\0panel-mipi-dbi-spi` + "\n" +
		"dtparam=width=320,height=240\n" +
		"dtparam=" + gpios + "\n" +
		keys.String() +
		blockEnd + "\n")
}

// CmdlineSettings are the kernel settings the console needs on every machine: no blinking
// cursor on the text console, no blanking it after ten minutes without a key, and a
// reboot ten seconds after a panic rather than a console that hangs.
var CmdlineSettings = []string{"vt.global_cursor_default=0", "consoleblank=0", "panic=10"}

// CmdlineTxt returns the Raspberry Pi's cmdline.txt. Kernel messages go to the panel and
// to the serial port, and /dev/console, where the dashboard and the games write, is the
// serial port (the last console named). There is no root file system: the initramfs is
// the system.
func CmdlineTxt() []byte {
	return []byte("console=tty1 console=serial0,115200 " + strings.Join(CmdlineSettings, " ") + "\n")
}
