// Package panel writes the start-up sequence of the console's ILI9341 panel in the form the
// Linux panel-mipi-dbi driver loads it.
//
// The mainline driver for a generic MIPI DBI panel knows nothing about any one controller:
// it sends whatever commands a firmware file lists, then drives the panel as a 320×240
// framebuffer. That file lives in /lib/firmware under the device tree's "compatible" name
// plus ".bin". Its format (drivers/gpu/drm/tiny/panel-mipi-dbi.c) is fifteen bytes of
// magic, a version byte, then commands as code, parameter count, parameters; a delay is the
// no-op command 0x00 with one parameter, in milliseconds.
//
// The sequence itself is the kernel's own for the ILI9341 (drivers/gpu/drm/tiny/ili9341.c),
// so a panel that works with that driver works here.
package panel

import (
	"bytes"
	"errors"
	"fmt"
)

// Compatible is the device-tree name the console gives its panel. The kernel loads the
// firmware by this name.
const Compatible = "vedutaos-ili9341"

// FirmwareName is the file the kernel looks for in /lib/firmware.
const FirmwareName = Compatible + ".bin"

var magic = []byte("MIPI DBI\x00\x00\x00\x00\x00\x00\x00")

const version = 1

// Command is one command sent to the controller. Code 0 with a single parameter is a delay
// of that many milliseconds.
type Command struct {
	Code   byte
	Params []byte
}

// Delay is a pause in the sequence, of 1 to 255 milliseconds.
func Delay(ms int) Command {
	return Command{Code: 0, Params: []byte{byte(ms)}}
}

// Options describe how a panel is mounted and wired.
type Options struct {
	// Rotate is how far the panel is turned, clockwise, to lie in landscape: 90 or 270.
	Rotate int
	// RGB says the panel's subpixels are in red-green-blue order. Most ILI9341 modules are
	// blue-green-red, which is the default.
	RGB bool
	// Invert turns on colour inversion, which many IPS modules (ILI9341V) need to show
	// colours the right way round.
	Invert bool
}

// MIPI DCS commands, and the ILI9341's own.
const (
	dcsExitSleep    = 0x11
	dcsInvertOn     = 0x21
	dcsGammaCurve   = 0x26
	dcsDisplayOff   = 0x28
	dcsDisplayOn    = 0x29
	dcsAddressMode  = 0x36
	dcsPixelFormat  = 0x3a
	pixelFormat16   = 0x55
	ili9341FRMCTR1  = 0xb1
	ili9341DISCTRL  = 0xb6
	ili9341ETMOD    = 0xb7
	ili9341PWCTRL1  = 0xc0
	ili9341PWCTRL2  = 0xc1
	ili9341VMCTRL1  = 0xc5
	ili9341VMCTRL2  = 0xc7
	ili9341PWCTRLA  = 0xcb
	ili9341PWCTRLB  = 0xcf
	ili9341PGAMCTRL = 0xe0
	ili9341NGAMCTRL = 0xe1
	ili9341DTCTRLA  = 0xe8
	ili9341DTCTRLB  = 0xea
	ili9341PWRSEQ   = 0xed
	ili9341EN3GAM   = 0xf2
	ili9341PUMPCTRL = 0xf7

	madctlBGR = 1 << 3
	madctlMV  = 1 << 5
	madctlMX  = 1 << 6
	madctlMY  = 1 << 7
)

// ILI9341 returns the start-up sequence for a panel mounted as o describes. The driver
// resets the panel before sending it.
func ILI9341(o Options) ([]Command, error) {
	var mode byte
	switch o.Rotate {
	case 90:
		mode = madctlMV
	case 270:
		mode = madctlMV | madctlMY | madctlMX
	default:
		return nil, fmt.Errorf("panel: rotate %d: the panel is 320×240, so it lies at 90 or 270", o.Rotate)
	}
	if !o.RGB {
		mode |= madctlBGR
	}
	cmds := []Command{
		{dcsDisplayOff, nil},
		{ili9341PWCTRLB, []byte{0x00, 0xc1, 0x30}},
		{ili9341PWRSEQ, []byte{0x64, 0x03, 0x12, 0x81}},
		{ili9341DTCTRLA, []byte{0x85, 0x00, 0x78}},
		{ili9341PWCTRLA, []byte{0x39, 0x2c, 0x00, 0x34, 0x02}},
		{ili9341PUMPCTRL, []byte{0x20}},
		{ili9341DTCTRLB, []byte{0x00, 0x00}},
		{ili9341PWCTRL1, []byte{0x23}},
		{ili9341PWCTRL2, []byte{0x10}},
		{ili9341VMCTRL1, []byte{0x3e, 0x28}},
		{ili9341VMCTRL2, []byte{0x86}},
		{dcsPixelFormat, []byte{pixelFormat16}},
		{ili9341FRMCTR1, []byte{0x00, 0x1b}},
		{ili9341EN3GAM, []byte{0x00}},
		{dcsGammaCurve, []byte{0x01}},
		{ili9341PGAMCTRL, []byte{0x0f, 0x31, 0x2b, 0x0c, 0x0e, 0x08, 0x4e, 0xf1, 0x37, 0x07, 0x10, 0x03, 0x0e, 0x09, 0x00}},
		{ili9341NGAMCTRL, []byte{0x00, 0x0e, 0x14, 0x03, 0x11, 0x07, 0x31, 0xc1, 0x48, 0x08, 0x0f, 0x0c, 0x31, 0x36, 0x0f}},
		{ili9341ETMOD, []byte{0x07}},
		{ili9341DISCTRL, []byte{0x08, 0x82, 0x27, 0x00}},
		// The kernel driver sets the address mode after the display is on; setting it
		// first keeps a turned picture from flashing for 100 ms.
		{dcsAddressMode, []byte{mode}},
	}
	if o.Invert {
		cmds = append(cmds, Command{dcsInvertOn, nil})
	}
	cmds = append(cmds,
		Command{dcsExitSleep, nil}, Delay(100),
		Command{dcsDisplayOn, nil}, Delay(100),
	)
	return cmds, nil
}

// Encode writes commands as a firmware file.
func Encode(cmds []Command) ([]byte, error) {
	var b bytes.Buffer
	b.Write(magic)
	b.WriteByte(version)
	for _, c := range cmds {
		if len(c.Params) > 255 {
			return nil, fmt.Errorf("panel: command %02x has %d parameters, more than 255", c.Code, len(c.Params))
		}
		if c.Code == 0 && len(c.Params) == 1 && c.Params[0] == 0 {
			return nil, errors.New("panel: a delay of 0 ms is not a delay")
		}
		b.WriteByte(c.Code)
		b.WriteByte(byte(len(c.Params)))
		b.Write(c.Params)
	}
	return b.Bytes(), nil
}

// Decode reads a firmware file back into commands.
func Decode(b []byte) ([]Command, error) {
	if len(b) < len(magic)+1 || !bytes.Equal(b[:len(magic)], magic) {
		return nil, errors.New("panel: not a MIPI DBI firmware file")
	}
	if v := b[len(magic)]; v != version {
		return nil, fmt.Errorf("panel: firmware version %d, want %d", v, version)
	}
	var cmds []Command
	for rest := b[len(magic)+1:]; len(rest) > 0; {
		if len(rest) < 2 || len(rest) < 2+int(rest[1]) {
			return nil, errors.New("panel: firmware ends inside a command")
		}
		n := int(rest[1])
		cmds = append(cmds, Command{Code: rest[0], Params: append([]byte(nil), rest[2:2+n]...)})
		rest = rest[2+n:]
	}
	return cmds, nil
}
