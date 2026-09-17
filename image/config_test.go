package image

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/riftbane/vedutaos/fdt"
)

func wired() *Wiring { return &Wiring{Pins: DefaultPins, Speed: DefaultSPISpeed} }

func TestConfigTxtChangesOnlyItsBlock(t *testing.T) {
	once := ConfigTxt(nil, wired())
	if !bytes.HasPrefix(once, []byte(stockConfig)) {
		t.Fatal("the image's own config.txt was not kept as it is")
	}
	if twice := ConfigTxt(once, wired()); !bytes.Equal(twice, once) {
		t.Fatalf("second run changed the file:\n%s", twice)
	}
	pins := DefaultPins
	pins.DC, pins.Reset, pins.Backlight, pins.Home = 22, -1, -1, -1
	other := &Wiring{Pins: pins, Speed: 16000000}
	changed := ConfigTxt(once, other)
	if bytes.Count(changed, []byte("# vedutaos begin")) != 1 || !bytes.Contains(changed, []byte("speed=16000000")) {
		t.Fatalf("block not replaced:\n%s", changed)
	}
	if !bytes.Contains(changed, []byte("dtparam=dc-gpio=22\n")) {
		t.Fatalf("unconnected lines should be left out:\n%s", changed)
	}
	if bytes.Count(changed, []byte("dtoverlay="+ButtonsOverlay+"\n")) != 1 || bytes.Contains(changed, []byte("gpio-key,")) {
		t.Fatalf("buttons: want the one overlay of them all:\n%s", changed)
	}
	pins.Up, pins.Down, pins.Left, pins.Right, pins.A, pins.B, pins.Select, pins.Cancel = -1, -1, -1, -1, -1, -1, -1, -1
	if none := ConfigTxt(once, &Wiring{Pins: pins, Speed: 16000000}); bytes.Contains(none, []byte(ButtonsOverlay)) {
		t.Fatalf("the buttons' overlay loaded with no button connected:\n%s", none)
	}
	if back := ConfigTxt(changed, nil); string(back) != stockConfig {
		t.Fatalf("taking the panel away did not give back the stock file:\n%s", back)
	}
	for _, want := range []string{"auto_initramfs=1", "arm_64bit=1", "dtoverlay=mipi-dbi-spi,spi0-0,speed=32000000,write-only", "dtparam=reset-gpio=25,dc-gpio=24,backlight-gpio=18"} {
		if !bytes.Contains(once, []byte(want)) {
			t.Errorf("config.txt lacks %q", want)
		}
	}
}

// testdata/bcm2712-rpi-5-b.dtb is the Pi 5's tree from the pinned kernel
// linux-image-6.18.50+rpt-rpi-2712.
func TestButtonsDTBO(t *testing.T) {
	pins := DefaultPins
	pins.Home = -1
	dtbo := ButtonsDTBO(pins)
	o, err := fdt.Parse(dtbo)
	if err != nil {
		t.Fatal(err)
	}
	keys := o.Find("/fragment@1/__overlay__/vedutaos-keys")
	if !bytes.Equal(prop(t, keys, "compatible"), fdt.Strings("gpio-keys")) || len(keys.Children) != 8 || keys.Child("button-home") != nil {
		t.Fatalf("want one gpio-keys device with the eight buttons connected: %+v", keys)
	}
	a := keys.Child("button-a")
	if !bytes.Equal(prop(t, a, "linux,code"), fdt.Cells(btnSouth)) || !bytes.Equal(prop(t, a, "gpios"), fdt.Cells(0xffffffff, 26, gpioActiveLow)) {
		t.Errorf("button-a: code %x gpios %x", prop(t, a, "linux,code"), prop(t, a, "gpios"))
	}
	if got := prop(t, o.Find("/__fixups__"), "gpio"); bytes.Count(got, []byte(":gpios:0")) != 8 || !bytes.HasPrefix(got, []byte("/fragment@0:target:0\x00")) {
		t.Errorf("fixups: %q", got)
	}

	// fdtoverlay, when it is here, applies it to the Pi 5's tree: the buttons point at RP1's
	// GPIO controller, and their pull-ups are a pin state of it.
	tool, err := exec.LookPath("fdtoverlay")
	if err != nil {
		return
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "buttons.dtbo"), dtbo, 0o644)
	out := filepath.Join(dir, "out.dtb")
	if msg, err := exec.Command(tool, "-i", filepath.Join("testdata", "bcm2712-rpi-5-b.dtb"), "-o", out, filepath.Join(dir, "buttons.dtbo")).CombinedOutput(); err != nil {
		t.Fatalf("fdtoverlay: %v\n%s", err, msg)
	}
	b, _ := os.ReadFile(out)
	tree, err := fdt.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	gpio := tree.Label("gpio")
	if !bytes.Equal(prop(t, gpio, "compatible"), fdt.Strings("raspberrypi,rp1-gpio")) {
		t.Fatalf("the Pi 5's gpio label is %q", prop(t, gpio, "compatible"))
	}
	applied := tree.Find("/vedutaos-keys")
	if !bytes.Equal(prop(t, applied.Child("button-a"), "gpios"), fdt.Cells(tree.Phandle(gpio), 26, gpioActiveLow)) {
		t.Errorf("button-a gpios %x", prop(t, applied.Child("button-a"), "gpios"))
	}
	if !bytes.Equal(prop(t, applied, "pinctrl-0"), fdt.Cells(tree.Phandle(gpio.Child("vedutaos-keys-pins")))) {
		t.Errorf("pinctrl-0 %x is not the pull-ups", prop(t, applied, "pinctrl-0"))
	}
}

func TestPinsCheck(t *testing.T) {
	if err := DefaultPins.Check(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Pins){
		"dc unconnected": func(p *Pins) { p.DC = -1 },
		"off the header": func(p *Pins) { p.A = 28 },
		"twice":          func(p *Pins) { p.B = p.A },
		"the SPI bus":    func(p *Pins) { p.Up = 10 },
		"the serial":     func(p *Pins) { p.Home = 14 },
	} {
		p := DefaultPins
		change(&p)
		if err := p.Check(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestConfigTxtWithoutTrailingNewline(t *testing.T) {
	got := ConfigTxt([]byte("dtparam=audio=on"), wired())
	if !bytes.HasPrefix(got, []byte("dtparam=audio=on\n# vedutaos begin")) {
		t.Fatalf("%s", got)
	}
}

func TestCmdlineTxt(t *testing.T) {
	got := string(CmdlineTxt())
	if strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
		t.Fatalf("cmdline.txt must be one line: %q", got)
	}
	// The serial port is named last, so /dev/console is the serial port.
	if !strings.HasPrefix(got, "console=tty1 console=serial0,115200 ") {
		t.Errorf("consoles: %q", got)
	}
	for _, s := range CmdlineSettings {
		if !strings.Contains(got, " "+s) {
			t.Errorf("%q lacks %s", got, s)
		}
	}
	if strings.Contains(got, "root=") {
		t.Errorf("there is no root file system: %q", got)
	}
}
