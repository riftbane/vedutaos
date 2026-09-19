package image

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/riftbane/vedutaos/fdt"
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

// Key codes of linux/input-event-codes.h the buttons report. The handheld's Select is the
// game's menu, which the engine reads from BTN_START: its BTN_SELECT is a USB pad's Select,
// which leaves the game.
const (
	btnDPadUp = 0x220 // BTN_DPAD_UP, then DOWN, LEFT, RIGHT
	btnSouth  = 0x130 // A
	btnEast   = 0x131 // B
	btnStart  = 0x13b // the menu (Select on the handheld)
	btnMode   = 0x13c // Home
	keyBack   = 158   // Cancel
)

// Lines lists every line of p, the panel's first, so that they can be set by name.
func (p *Pins) Lines() []Line {
	return []Line{
		{"dc", &p.DC, 0}, {"reset", &p.Reset, 0}, {"backlight", &p.Backlight, 0},
		{"up", &p.Up, btnDPadUp}, {"down", &p.Down, btnDPadUp + 1}, {"left", &p.Left, btnDPadUp + 2}, {"right", &p.Right, btnDPadUp + 3},
		{"a", &p.A, btnSouth}, {"b", &p.B, btnEast}, {"select", &p.Select, btnStart}, {"cancel", &p.Cancel, keyBack}, {"home", &p.Home, btnMode},
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
	keys := ""
	if len(w.Pins.buttons()) > 0 {
		keys = "dtoverlay=" + ButtonsOverlay + "\n"
	}
	return []byte(out + blockBegin + "\n" +
		"[all]\n" +
		"dtparam=spi=on\n" +
		"dtoverlay=mipi-dbi-spi,spi0-0,speed=" + strconv.Itoa(w.Speed) + ",write-only\n" +
		"dtparam=compatible=" + panel.Compatible + `\0panel-mipi-dbi-spi` + "\n" +
		"dtparam=width=320,height=240\n" +
		"dtparam=" + gpios + "\n" +
		keys +
		blockEnd + "\n")
}

// ButtonsOverlay names the overlay of the buttons on the Raspberry Pis, in overlays/ on the
// card.
const ButtonsOverlay = "vedutaos-buttons"

// buttons returns the buttons that are connected.
func (p *Pins) buttons() []Line {
	var b []Line
	for _, l := range p.Lines() {
		if l.Code != 0 && *l.GPIO >= 0 {
			b = append(b, l)
		}
	}
	return b
}

// ButtonsDTBO returns the Raspberry Pi overlay of the buttons: one gpio-keys device holding
// every connected button, pulled up and active low, as on the Orange Pi (Zero2WTree). The
// stock gpio-key overlay makes a device per button, and a device with a single button
// other than A or Up does not look like a pad, so the player would not read it. The
// overlay refers to the board's GPIO controller by its label, gpio, which every Pi's tree
// has (on the Pi 5 it is RP1's, whose driver reads the same brcm,* pin properties).
func ButtonsDTBO(p Pins) []byte {
	const gpioPhandle = 0xffffffff // resolved through __fixups__
	t := &fdt.Tree{Root: &fdt.Node{}}
	root := t.Root
	root.Set("compatible", fdt.Strings("brcm,bcm2835"))

	var pins, functions, pulls []uint32
	buttons := p.buttons()
	for _, l := range buttons {
		pins = append(pins, uint32(*l.GPIO))
		functions = append(functions, 0) // input
		pulls = append(pulls, 2)         // up
	}
	pull := root.Add("fragment@0")
	pull.Set("target", fdt.Cells(gpioPhandle))
	pinNode := pull.Add("__overlay__").Add("vedutaos-keys-pins")
	pinNode.Set("brcm,pins", fdt.Cells(pins...))
	pinNode.Set("brcm,function", fdt.Cells(functions...))
	pinNode.Set("brcm,pull", fdt.Cells(pulls...))
	pinNode.Set("phandle", fdt.Cells(1))

	keysFragment := root.Add("fragment@1")
	keysFragment.Set("target-path", fdt.Strings("/"))
	keys := keysFragment.Add("__overlay__").Add("vedutaos-keys")
	keys.Set("compatible", fdt.Strings("gpio-keys"))
	keys.Set("pinctrl-names", fdt.Strings("default"))
	keys.Set("pinctrl-0", fdt.Cells(1))
	gpioUsers := []string{"/fragment@0:target:0"}
	for _, l := range buttons {
		b := keys.Add("button-" + l.Name)
		b.Set("label", fdt.Strings(l.Name))
		b.Set("linux,code", fdt.Cells(l.Code))
		b.Set("gpios", fdt.Cells(gpioPhandle, uint32(*l.GPIO), gpioActiveLow))
		gpioUsers = append(gpioUsers, "/fragment@1/__overlay__/vedutaos-keys/button-"+l.Name+":gpios:0")
	}
	root.Add("__fixups__").Set("gpio", fdt.Strings(gpioUsers...))
	root.Add("__local_fixups__").Add("fragment@1").Add("__overlay__").Add("vedutaos-keys").Set("pinctrl-0", fdt.Cells(0))
	return t.Bytes()
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
