package image

import (
	"bytes"
	"strings"
	"testing"
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
	other := &Wiring{Pins: Pins{DC: 22, Reset: -1, Backlight: -1}, Speed: 16000000}
	changed := ConfigTxt(once, other)
	if bytes.Count(changed, []byte("# vedutaos begin")) != 1 || !bytes.Contains(changed, []byte("speed=16000000")) {
		t.Fatalf("block not replaced:\n%s", changed)
	}
	if !bytes.Contains(changed, []byte("dtparam=dc-gpio=22\n")) {
		t.Fatalf("unconnected lines should be left out:\n%s", changed)
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
