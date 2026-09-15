package image

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func stock(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "stock", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func wired() *Wiring { return &Wiring{Pins: DefaultPins, Speed: DefaultSPISpeed} }

func TestConfigTxtChangesOnlyItsBlock(t *testing.T) {
	orig := stock(t, "config.txt")
	once := ConfigTxt(orig, wired())
	if !bytes.HasPrefix(once, orig) {
		t.Fatal("the rest of config.txt was not kept as it was")
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
	if back := ConfigTxt(changed, nil); !bytes.Equal(back, orig) {
		t.Fatalf("taking the panel away did not give back the stock file:\n%s", back)
	}
}

func TestConfigTxtWithoutTrailingNewline(t *testing.T) {
	got := ConfigTxt([]byte("dtparam=audio=on"), wired())
	if !bytes.HasPrefix(got, []byte("dtparam=audio=on\n# vedutaos begin")) {
		t.Fatalf("%s", got)
	}
}

func TestCmdline(t *testing.T) {
	orig := stock(t, "cmdline.txt")
	once, err := Cmdline(orig)
	if err != nil {
		t.Fatal(err)
	}
	if twice, _ := Cmdline(once); !bytes.Equal(twice, once) {
		t.Fatalf("not idempotent: %q then %q", once, twice)
	}
	if !bytes.HasPrefix(once, bytes.TrimRight(orig, "\n")) {
		t.Fatalf("existing settings moved or lost: %q", once)
	}
	got, _ := Cmdline([]byte("console=tty1 consoleblank=600 root=/dev/mmcblk0p2\r\n"))
	if string(got) != "console=tty1 root=/dev/mmcblk0p2 vt.global_cursor_default=0 consoleblank=0\n" {
		t.Fatalf("%q", got)
	}
	for _, bad := range []string{"", "\n", "console=tty1\nroot=/dev/sda2\n"} {
		if _, err := Cmdline([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
