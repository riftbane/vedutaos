package panel

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update-golden", false, "rewrite the reference firmware")

// The firmware the console ships by default is pinned byte for byte: a change to it is a
// change to what the glass shows, and must be looked at on a panel before it is accepted.
func TestDefaultFirmwareGolden(t *testing.T) {
	cmds, err := ILI9341(Options{Rotate: 90})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Encode(cmds)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", FirmwareName)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update-golden once the firmware is right)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("firmware differs from %s (run with -update-golden once it is right)", path)
	}
}

// The header is what the kernel checks before it reads a single command
// (drivers/gpu/drm/tiny/panel-mipi-dbi.c): fifteen bytes of magic, then version 1.
func TestHeader(t *testing.T) {
	b, err := Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte("MIPI DBI\x00\x00\x00\x00\x00\x00\x00"), 1)
	if !bytes.Equal(b, want) {
		t.Fatalf("header % x, want % x", b, want)
	}
}

func TestRoundTrip(t *testing.T) {
	for _, o := range []Options{{Rotate: 90}, {Rotate: 270, RGB: true, Invert: true}} {
		cmds, err := ILI9341(o)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Encode(cmds)
		if err != nil {
			t.Fatal(err)
		}
		back, err := Decode(b)
		if err != nil {
			t.Fatal(err)
		}
		if len(back) != len(cmds) {
			t.Fatalf("%+v: %d commands back, want %d", o, len(back), len(cmds))
		}
		for i := range cmds {
			if back[i].Code != cmds[i].Code || !bytes.Equal(back[i].Params, cmds[i].Params) {
				t.Fatalf("%+v: command %d is %+v, want %+v", o, i, back[i], cmds[i])
			}
		}
	}
}

// find returns the parameters of the first command with a code, and whether there was one.
func find(cmds []Command, code byte) ([]byte, bool) {
	for _, c := range cmds {
		if c.Code == code {
			return c.Params, true
		}
	}
	return nil, false
}

func TestWhatThePanelIsTold(t *testing.T) {
	tests := []struct {
		o       Options
		madctl  byte
		inverts bool
	}{
		{Options{Rotate: 90}, 0x28, false},
		{Options{Rotate: 270}, 0xe8, false},
		{Options{Rotate: 90, RGB: true}, 0x20, false},
		{Options{Rotate: 90, Invert: true}, 0x28, true},
	}
	for _, tc := range tests {
		cmds, err := ILI9341(tc.o)
		if err != nil {
			t.Fatal(err)
		}
		if p, _ := find(cmds, 0x3a); !bytes.Equal(p, []byte{0x55}) {
			t.Errorf("%+v: pixel format % x, want 55 (RGB565, what the framebuffer is packed as)", tc.o, p)
		}
		if p, _ := find(cmds, 0x36); !bytes.Equal(p, []byte{tc.madctl}) {
			t.Errorf("%+v: address mode % x, want %02x", tc.o, p, tc.madctl)
		}
		if _, ok := find(cmds, 0x21); ok != tc.inverts {
			t.Errorf("%+v: inversion sent %v, want %v", tc.o, ok, tc.inverts)
		}
		// Sleep out and display on each need time before the next command.
		for _, code := range []byte{0x11, 0x29} {
			i := indexOf(cmds, code)
			if i < 0 || i+1 >= len(cmds) || cmds[i+1].Code != 0 || len(cmds[i+1].Params) != 1 || cmds[i+1].Params[0] < 100 {
				t.Errorf("%+v: command %02x is not followed by a delay of at least 100 ms", tc.o, code)
			}
		}
	}
}

func indexOf(cmds []Command, code byte) int {
	for i, c := range cmds {
		if c.Code == code {
			return i
		}
	}
	return -1
}

// A panel standing on its end is not 320×240: only the two landscape turns are offered.
func TestRotateMustBeLandscape(t *testing.T) {
	for _, r := range []int{0, 180, 45, -90} {
		if _, err := ILI9341(Options{Rotate: r}); err == nil {
			t.Errorf("rotate %d accepted", r)
		}
	}
}

func TestEncodeRefusesWhatTheFormatCannotHold(t *testing.T) {
	if _, err := Encode([]Command{{Code: 0x2c, Params: make([]byte, 256)}}); err == nil {
		t.Error("a command with 256 parameters was encoded")
	}
	if _, err := Encode([]Command{Delay(0)}); err == nil {
		t.Error("a delay of 0 ms was encoded")
	}
}

func TestDecodeRefusesDamage(t *testing.T) {
	good, _ := Encode([]Command{{Code: 0x11}, Delay(120)})
	for name, b := range map[string][]byte{
		"short":     good[:10],
		"magic":     append([]byte("MIPI DBX"), good[8:]...),
		"version":   append(append([]byte{}, good[:15]...), append([]byte{2}, good[16:]...)...),
		"truncated": good[:len(good)-1],
	} {
		if _, err := Decode(b); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}

// The kernel keeps the firmware name in 40 bytes, terminator included.
func TestFirmwareNameFits(t *testing.T) {
	if len(FirmwareName) > 39 {
		t.Fatalf("%q is longer than the kernel's 39 characters", FirmwareName)
	}
}
