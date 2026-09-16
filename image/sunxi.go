package image

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/riftbane/vedutaos/fdt"
	"github.com/riftbane/vedutaos/panel"
)

// The Orange Pi Zero 2W (Allwinner H618) boots from the same card as the Raspberry Pis. Its
// boot ROM reads U-Boot from 8 KiB into the card, below the partition, and U-Boot reads
// extlinux/extlinux.conf from the partition: Armbian's kernel, the console's initramfs for
// it, and the board's device tree with the panel and the buttons written in (Zero2WTree).
const (
	// Zero2WDTB is the board's device tree, as the kernel package names it.
	Zero2WDTB = "sun50i-h618-orangepi-zero2w.dtb"
	// SunxiDir is the folder on the card holding what U-Boot loads.
	SunxiDir = "sunxi"
	// ubootOffset is where the boot ROM looks for U-Boot.
	ubootOffset = 8 << 10
)

// sunxiModules are what the console needs on the H618 besides what Armbian's kernel builds
// in (the card, SPI, USB host, HID): the panel, its backlight, the buttons, and the USB OTG
// port.
var sunxiModules = []string{"vfat", "nls_cp437", "usbhid", "hid-generic", "evdev", "panel-mipi-dbi", "gpio_backlight", "gpio_keys", "sunxi"}

// zero2wHeader maps the Raspberry Pi GPIO numbers of the 40-pin header to the H618 pins on
// the same header pins (the Orange Pi Zero 2W schematic).
var zero2wHeader = map[int]string{
	0: "PI10", 1: "PI9", 2: "PI8", 3: "PI7", 4: "PI13", 5: "PI0", 6: "PI15", 7: "PH9",
	8: "PH5", 9: "PH8", 10: "PH7", 11: "PH6", 12: "PI11", 13: "PI12", 14: "PH0", 15: "PH1",
	16: "PC12", 17: "PH2", 18: "PI1", 19: "PI2", 20: "PI4", 21: "PI3", 22: "PI5", 23: "PI14",
	24: "PH4", 25: "PI6", 26: "PI16", 27: "PH3",
}

// Flags of a GPIO specifier (dt-bindings/gpio/gpio.h).
const gpioActiveLow = 1

// Zero2WTree returns the board's device tree with the console written in: SPI1 (the
// header's SPI, chip select 0) on with the panel on it, as the Raspberry Pi's mipi-dbi-spi
// overlay describes it; the backlight on a GPIO; the buttons as gpio-keys, pulled up and
// active low; and HDMI off, so the panel is the only screen and draws no power for a port
// the console does not have. The tree must have its labels (__symbols__), as Armbian builds
// it.
func Zero2WTree(dtb []byte, w Wiring) ([]byte, error) {
	if err := w.Pins.Check(); err != nil {
		return nil, err
	}
	t, err := fdt.Parse(dtb)
	if err != nil {
		return nil, err
	}
	nodes := map[string]*fdt.Node{}
	for _, label := range []string{"pio", "spi1", "spi1_pins", "spi1_cs0_pin"} {
		if nodes[label] = t.Label(label); nodes[label] == nil {
			return nil, fmt.Errorf("%s: the device tree has no %s", Zero2WDTB, label)
		}
	}
	for _, label := range []string{"de", "hdmi"} {
		if n := t.Label(label); n != nil {
			n.Set("status", fdt.Strings("disabled"))
		}
	}
	pio := t.Phandle(nodes["pio"])
	gpio := func(line int, flags uint32) []byte {
		bank, pin := sunxiPin(zero2wHeader[line])
		return fdt.Cells(pio, bank, pin, flags)
	}

	spi := nodes["spi1"]
	spi.Set("status", fdt.Strings("okay"))
	spi.Set("pinctrl-names", fdt.Strings("default"))
	spi.Set("pinctrl-0", fdt.Cells(t.Phandle(nodes["spi1_pins"]), t.Phandle(nodes["spi1_cs0_pin"])))
	spi.Set("#address-cells", fdt.Cells(1))
	spi.Set("#size-cells", fdt.Cells(0))
	p := spi.Add("panel@0")
	p.Set("compatible", fdt.Strings(panel.Compatible, "panel-mipi-dbi-spi"))
	p.Set("reg", fdt.Cells(0))
	p.Set("spi-max-frequency", fdt.Cells(uint32(w.Speed)))
	p.Set("write-only", nil)
	p.Set("width-mm", fdt.Cells(0))
	p.Set("height-mm", fdt.Cells(0))
	p.Set("dc-gpios", gpio(w.Pins.DC, 0))
	if w.Pins.Reset >= 0 {
		p.Set("reset-gpios", gpio(w.Pins.Reset, 0))
	}
	timing := p.Add("panel-timing")
	timing.Set("hactive", fdt.Cells(320))
	timing.Set("vactive", fdt.Cells(240))
	for _, name := range []string{"hback-porch", "vback-porch", "clock-frequency", "hfront-porch", "hsync-len", "vfront-porch", "vsync-len"} {
		timing.Set(name, fdt.Cells(0))
	}
	if w.Pins.Backlight >= 0 {
		bl := t.Root.Add("vedutaos-backlight")
		bl.Set("compatible", fdt.Strings("gpio-backlight"))
		bl.Set("gpios", gpio(w.Pins.Backlight, 0))
		p.Set("backlight", fdt.Cells(t.Phandle(bl)))
	}

	var buttons []Line
	var pins []string
	for _, l := range w.Pins.Lines() {
		if l.Code != 0 && *l.GPIO >= 0 {
			buttons = append(buttons, l)
			pins = append(pins, zero2wHeader[*l.GPIO])
		}
	}
	if len(buttons) > 0 {
		// The pull-ups are set by the pin controller, which every kernel honours, rather than
		// by a flag of the GPIO specifier.
		pull := nodes["pio"].Add("vedutaos-keys-pins")
		pull.Set("pins", fdt.Strings(pins...))
		pull.Set("function", fdt.Strings("gpio_in"))
		pull.Set("bias-pull-up", nil)
		keys := t.Root.Add("vedutaos-keys")
		keys.Set("compatible", fdt.Strings("gpio-keys"))
		keys.Set("pinctrl-names", fdt.Strings("default"))
		keys.Set("pinctrl-0", fdt.Cells(t.Phandle(pull)))
		for _, l := range buttons {
			b := keys.Add("button-" + l.Name)
			b.Set("label", fdt.Strings(l.Name))
			b.Set("linux,code", fdt.Cells(l.Code))
			b.Set("gpios", gpio(*l.GPIO, gpioActiveLow))
		}
	}
	return t.Bytes(), nil
}

// sunxiPin splits a pin name such as PH4 into its bank (H is 7) and number.
func sunxiPin(name string) (bank, pin uint32) {
	n, _ := strconv.Atoi(name[2:])
	return uint32(name[1] - 'A'), uint32(n)
}

// ExtlinuxConf is the boot menu U-Boot reads: one entry, booted at once. Kernel messages go
// to the panel and to the serial port (UART0, header pins 8 and 10), and /dev/console is the
// serial port, as on the Raspberry Pis.
func ExtlinuxConf() []byte {
	return []byte("# VedutaOS on the Orange Pi Zero 2W, read by U-Boot.\n" +
		"default vedutaos\n" +
		"prompt 0\n" +
		"timeout 1\n\n" +
		"label vedutaos\n" +
		"\tkernel /" + SunxiDir + "/Image\n" +
		"\tinitrd /" + SunxiDir + "/initrd.img\n" +
		"\tfdt /" + SunxiDir + "/" + Zero2WDTB + "\n" +
		"\tappend console=tty1 console=ttyS0,115200 " + strings.Join(CmdlineSettings, " ") + "\n")
}

// checkUBoot refuses a U-Boot that would not fit between its offset and the partition.
func checkUBoot(b []byte) error {
	if len(b) == 0 {
		return errors.New("u-boot-sunxi-with-spl.bin is empty")
	}
	if room := partitionLBA*sectorSize - ubootOffset; len(b) > room {
		return fmt.Errorf("u-boot-sunxi-with-spl.bin is %d bytes, and there are %d before the partition", len(b), room)
	}
	return nil
}
