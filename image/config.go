package image

import (
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
	return []byte(out + blockBegin + "\n" +
		"[all]\n" +
		"dtparam=spi=on\n" +
		"dtoverlay=mipi-dbi-spi,spi0-0,speed=" + strconv.Itoa(w.Speed) + ",write-only\n" +
		"dtparam=compatible=" + panel.Compatible + `\0panel-mipi-dbi-spi` + "\n" +
		"dtparam=width=320,height=240\n" +
		"dtparam=" + gpios + "\n" +
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
