package image

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/riftbane/vedutaos/fdt"
	"github.com/riftbane/vedutaos/panel"
)

// testdata/sun50i-h618-orangepi-zero2w.dtb is the board's tree from Armbian's kernel 6.18.44
// (linux-image-current-sunxi64 26.8.3), the one the image pins.
func zero2w(t *testing.T, w Wiring) *fdt.Tree {
	t.Helper()
	base, err := os.ReadFile(filepath.Join("testdata", Zero2WDTB))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Zero2WTree(base, w)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := fdt.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func prop(t *testing.T, n *fdt.Node, name string) []byte {
	t.Helper()
	if n == nil {
		t.Fatalf("no node for %s", name)
	}
	v, ok := n.Prop(name)
	if !ok {
		t.Fatalf("%s has no %s", n.Name, name)
	}
	return v
}

// byPhandle finds the node a phandle names.
func byPhandle(tree *fdt.Tree, h uint32) *fdt.Node {
	var found *fdt.Node
	var walk func(*fdt.Node)
	walk = func(n *fdt.Node) {
		if v, ok := n.Prop("phandle"); ok && binary.BigEndian.Uint32(v) == h {
			found = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(tree.Root)
	return found
}

func TestZero2WTree(t *testing.T) {
	tree := zero2w(t, Wiring{Pins: DefaultPins, Speed: DefaultSPISpeed})
	spi := tree.Find("/soc/spi@5011000")
	if string(prop(t, spi, "status")) != "okay\x00" {
		t.Error("SPI1 is not on")
	}
	pc := prop(t, spi, "pinctrl-0")
	if len(pc) != 8 || byPhandle(tree, binary.BigEndian.Uint32(pc)).Name != "spi1-pins" || byPhandle(tree, binary.BigEndian.Uint32(pc[4:])).Name != "spi1-cs0-pin" {
		t.Errorf("SPI1's pins: %x", pc)
	}
	p := spi.Child("panel@0")
	if !bytes.Equal(prop(t, p, "compatible"), fdt.Strings(panel.Compatible, "panel-mipi-dbi-spi")) || !bytes.Equal(prop(t, p, "spi-max-frequency"), fdt.Cells(DefaultSPISpeed)) {
		t.Error("the panel is not the console's")
	}
	pio := binary.BigEndian.Uint32(prop(t, tree.Label("pio"), "phandle"))
	// GPIO 24 is header pin 18, PH4; 25 is pin 22, PI6; 18 is pin 12, PI1.
	for name, want := range map[string][]byte{"dc-gpios": fdt.Cells(pio, 7, 4, 0), "reset-gpios": fdt.Cells(pio, 8, 6, 0)} {
		if got := prop(t, p, name); !bytes.Equal(got, want) {
			t.Errorf("%s %x, want %x", name, got, want)
		}
	}
	if got := prop(t, p.Child("panel-timing"), "hactive"); !bytes.Equal(got, fdt.Cells(320)) {
		t.Errorf("hactive %x", got)
	}
	bl := byPhandle(tree, binary.BigEndian.Uint32(prop(t, p, "backlight")))
	if bl == nil || !bytes.Equal(prop(t, bl, "gpios"), fdt.Cells(pio, 8, 1, 0)) {
		t.Errorf("backlight %+v", bl)
	}

	for _, path := range []string{"/display-engine", "/soc/hdmi@6000000"} {
		if string(prop(t, tree.Find(path), "status")) != "disabled\x00" {
			t.Errorf("%s is not off", path)
		}
	}

	keys := tree.Find("/vedutaos-keys")
	if string(prop(t, keys, "compatible")) != "gpio-keys\x00" || len(keys.Children) != 9 {
		t.Fatalf("keys %+v", keys)
	}
	pull := byPhandle(tree, binary.BigEndian.Uint32(prop(t, keys, "pinctrl-0")))
	if pull == nil || !strings.Contains(string(prop(t, pull, "pins")), "PC12\x00") {
		t.Fatalf("the buttons' pull-ups: %+v", pull)
	}
	if _, ok := pull.Prop("bias-pull-up"); !ok {
		t.Error("the buttons are not pulled up")
	}
	// A is GPIO 26, header pin 37, PI16; Home is GPIO 12, pin 32, PI11.
	for name, want := range map[string][2][]byte{
		"button-a":    {fdt.Cells(btnSouth), fdt.Cells(pio, 8, 16, gpioActiveLow)},
		"button-home": {fdt.Cells(btnMode), fdt.Cells(pio, 8, 11, gpioActiveLow)},
		"button-up":   {fdt.Cells(btnDPadUp), fdt.Cells(pio, 8, 0, gpioActiveLow)},
	} {
		b := keys.Child(name)
		if !bytes.Equal(prop(t, b, "linux,code"), want[0]) || !bytes.Equal(prop(t, b, "gpios"), want[1]) {
			t.Errorf("%s: code %x gpios %x", name, prop(t, b, "linux,code"), prop(t, b, "gpios"))
		}
	}

	// Without the optional lines, nothing is written for them.
	pins := DefaultPins
	pins.Reset, pins.Backlight = -1, -1
	for _, l := range pins.Lines() {
		if l.Code != 0 {
			*l.GPIO = -1
		}
	}
	bare := zero2w(t, Wiring{Pins: pins, Speed: DefaultSPISpeed})
	p = bare.Find("/soc/spi@5011000/panel@0")
	for _, name := range []string{"reset-gpios", "backlight"} {
		if _, ok := p.Prop(name); ok {
			t.Errorf("%s written for a line not connected", name)
		}
	}
	if bare.Find("/vedutaos-keys") != nil || bare.Find("/vedutaos-backlight") != nil {
		t.Error("nodes written for lines not connected")
	}

	// dtc, when it is here, reads it back without complaint about references.
	if dtc, err := exec.LookPath("dtc"); err == nil {
		base, _ := os.ReadFile(filepath.Join("testdata", Zero2WDTB))
		out, _ := Zero2WTree(base, Wiring{Pins: DefaultPins, Speed: DefaultSPISpeed})
		cmd := exec.Command(dtc, "-I", "dtb", "-O", "dts", "-o", os.DevNull, "-")
		cmd.Stdin = bytes.NewReader(out)
		if msg, err := cmd.CombinedOutput(); err != nil || bytes.Contains(msg, []byte("ERROR")) {
			t.Errorf("dtc: %v\n%s", err, msg)
		}
	}
}

func TestZero2WTreeRefuses(t *testing.T) {
	base, _ := os.ReadFile(filepath.Join("testdata", Zero2WDTB))
	pins := DefaultPins
	pins.A = pins.B
	if _, err := Zero2WTree(base, Wiring{Pins: pins, Speed: DefaultSPISpeed}); err == nil {
		t.Error("a line used twice was accepted")
	}
	small, _ := os.ReadFile(filepath.Join("..", "fdt", "testdata", "small.dtb"))
	if _, err := Zero2WTree(small, Wiring{Pins: DefaultPins, Speed: DefaultSPISpeed}); err == nil || !strings.Contains(err.Error(), "spi1_pins") {
		t.Errorf("a tree without the board's labels: %v", err)
	}
}

func TestExtlinuxConf(t *testing.T) {
	got := string(ExtlinuxConf())
	for _, want := range []string{"default vedutaos\n", "\tkernel /sunxi/Image\n", "\tinitrd /sunxi/initrd.img\n", "\tfdt /sunxi/" + Zero2WDTB + "\n", " console=ttyS0,115200 "} {
		if !strings.Contains(got, want) {
			t.Errorf("extlinux.conf lacks %q:\n%s", want, got)
		}
	}
	for _, s := range CmdlineSettings {
		if !strings.Contains(got, " "+s) {
			t.Errorf("extlinux.conf lacks %s", s)
		}
	}
}
