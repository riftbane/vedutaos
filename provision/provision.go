// Package provision writes the files that turn a stock Linux image into the console on its
// first boot.
//
// Nothing here runs on the console. A PC writes these files onto the card's FAT partition —
// the Raspberry Pi's boot partition, or the folder QEMU shows the guest as a disk labelled
// CIDATA — and cloud-init, which Raspberry Pi OS and Debian's generic images both carry,
// reads them when the image first starts:
//
//	user-data, meta-data            what cloud-init does, once per change of the card
//	userconf.txt                    Raspberry Pi OS: the first user, so no wizard asks for one
//	config.txt, cmdline.txt         Raspberry Pi: the panel's overlay, a console with no cursor
//	vedutaos/vshell                 the dashboard (written by the caller)
//	vedutaos/env                    the dashboard's settings, read each time it starts
//	vedutaos/vedutaos-ili9341.bin   the panel's start-up sequence, copied to /lib/firmware
//	games/                          the games, as a PC drops them
//
// cloud-init installs a systemd unit that runs the dashboard from the card itself on the
// first virtual terminal, so replacing vshell on the card from a PC updates the console.
//
// user-data is YAML, and the standard library has no YAML encoder; but JSON is YAML, so the
// document is written with encoding/json after the "#cloud-config" line and can never be
// mis-indented or mis-quoted.
package provision

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/riftbane/vedutaos/panel"
)

// Target is the kind of machine a card is for.
type Target string

const (
	// Pi is a Raspberry Pi running Raspberry Pi OS Lite (64-bit), Trixie or later.
	Pi Target = "pi"
	// QEMU is Debian's generic arm64 cloud image on QEMU's virt machine.
	QEMU Target = "qemu"
)

// Where things are, on the card and on the console.
const (
	Mount        = "/boot/firmware" // where the console sees the card's FAT partition
	Dir          = "vedutaos"
	VShell       = Dir + "/vshell"
	EnvFile      = Dir + "/env"
	Firmware     = Dir + "/" + panel.FirmwareName
	Games        = "games"
	UserDataFile = "user-data"
	MetaDataFile = "meta-data"
	UserConfFile = "userconf.txt"
	ConfigFile   = "config.txt"
	CmdlineFile  = "cmdline.txt"
	UnitName     = "vedutaos.service"
	User         = "veduta"
	Label        = "CIDATA" // the label cloud-init looks for on QEMU's card disk
)

// QEMUPassword is the password of the veduta user in the emulator, whose ssh port is only
// forwarded on the host's loopback address. The Raspberry Pi's user has none.
const QEMUPassword = "veduta"

// marker is the line that says a file was written here and may be written again.
const marker = "# Written by vedutaos card, which replaces this file each time it runs."

// Pins are the Raspberry Pi GPIO lines (BCM numbering) the panel is wired to. A negative
// Reset or Backlight means the line is not connected.
type Pins struct {
	DC, Reset, Backlight int
}

// DefaultPins is the wiring the documentation shows.
var DefaultPins = Pins{DC: 24, Reset: 25, Backlight: 18}

// DefaultSPISpeed is the SPI clock of the panel, in hertz.
const DefaultSPISpeed = 32000000

// Options describe the console a card makes.
type Options struct {
	Target   Target
	Panel    bool     // Pi: an ILI9341 is wired to SPI0; otherwise the dashboard uses HDMI
	Pins     Pins     // Pi with a panel
	SPISpeed int      // Pi with a panel, in hertz
	Scale    int      // VEDUTA_SCALE: how many panel pixels each rendered pixel covers
	FB       string   // VEDUTA_FB, empty to let the engine choose
	Pad      string   // VEDUTA_PAD, empty to read every pad and keyboard
	SSHKeys  []string // public keys allowed to log in as the veduta user
}

// Validate reports the first thing about o that cannot make a console.
func (o Options) Validate() error {
	switch o.Target {
	case Pi:
		if o.Panel {
			if o.Pins.DC < 0 || o.Pins.DC > 53 {
				return fmt.Errorf("provision: the panel needs its DC line on a GPIO from 0 to 53, not %d", o.Pins.DC)
			}
			if o.Pins.Reset > 53 || o.Pins.Backlight > 53 {
				return errors.New("provision: GPIO lines go from 0 to 53")
			}
			if o.SPISpeed <= 0 {
				return fmt.Errorf("provision: SPI speed %d Hz", o.SPISpeed)
			}
		}
	case QEMU:
		if o.Panel {
			return errors.New("provision: QEMU has no SPI panel to drive")
		}
	default:
		return fmt.Errorf("provision: unknown target %q (want %s or %s)", o.Target, Pi, QEMU)
	}
	if o.Scale < 1 || o.Scale > 8 {
		return fmt.Errorf("provision: scale %d, want 1 to 8", o.Scale)
	}
	for _, v := range []string{o.FB, o.Pad} {
		if strings.ContainsAny(v, "\n\r\"'\\$ ") {
			return fmt.Errorf("provision: %q cannot be written to the settings file", v)
		}
	}
	for _, k := range o.SSHKeys {
		if strings.ContainsAny(k, "\n\r") || !strings.HasPrefix(k, "ssh-") && !strings.HasPrefix(k, "ecdsa-") && !strings.HasPrefix(k, "sk-") {
			return fmt.Errorf("provision: %.20q… is not one OpenSSH public key", k)
		}
	}
	return nil
}

type cloudConfig struct {
	Hostname   string      `json:"hostname"`
	Users      []user      `json:"users"`
	SSHPwauth  bool        `json:"ssh_pwauth"`
	WriteFiles []file      `json:"write_files"`
	Runcmd     []string    `json:"runcmd"`
	PowerState *powerState `json:"power_state,omitempty"`
}

type user struct {
	Name            string `json:"name"`
	Gecos           string `json:"gecos"`
	Groups          string `json:"groups"`
	Shell           string `json:"shell"`
	Sudo            string `json:"sudo"`
	LockPasswd      bool   `json:"lock_passwd"`
	PlainTextPasswd string `json:"plain_text_passwd"`
}

type file struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	Permissions string `json:"permissions"`
}

type powerState struct {
	Mode      string `json:"mode"`
	Message   string `json:"message"`
	Condition bool   `json:"condition"`
}

// The file that lets the listed keys log in as the veduta user. It is kept outside the
// user's home because on a Raspberry Pi that home is renamed during the same first boot.
const (
	authorizedKeys = "/etc/ssh/vedutaos_authorized_keys/" + User
	sshdDropIn     = "/etc/ssh/sshd_config.d/vedutaos.conf"
)

// UserData returns cloud-init's user-data for o.
func UserData(o Options) ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	cc := cloudConfig{
		Hostname:   "vedutaos",
		Users:      []user{}, // Raspberry Pi OS renames its own first user (userconf.txt)
		WriteFiles: []file{{Path: "/etc/systemd/system/" + UnitName, Content: string(Unit()), Permissions: "0644"}},
	}
	if o.Target == QEMU {
		cc.Users = []user{{
			Name: User, Gecos: "VedutaOS", Groups: "sudo,video,input", Shell: "/bin/bash",
			Sudo: "ALL=(ALL) NOPASSWD:ALL", PlainTextPasswd: QEMUPassword,
		}}
		cc.SSHPwauth = true
		// The card is a disk QEMU makes from a folder; mount it where a Raspberry Pi has its
		// boot partition, so the unit and the settings are the same on both.
		cc.Runcmd = append(cc.Runcmd,
			"mkdir -p "+Mount,
			"grep -q ' "+Mount+" ' /etc/fstab || echo 'LABEL="+Label+" "+Mount+" vfat ro,nofail 0 0' >> /etc/fstab",
			"mountpoint -q "+Mount+" || mount "+Mount,
		)
	}
	if o.Target == Pi && o.Panel {
		cc.Runcmd = append(cc.Runcmd, "install -D -m 0644 "+Mount+"/"+Firmware+" /lib/firmware/"+panel.FirmwareName)
	}
	if len(o.SSHKeys) > 0 {
		cc.WriteFiles = append(cc.WriteFiles,
			file{Path: authorizedKeys, Content: strings.Join(o.SSHKeys, "\n") + "\n", Permissions: "0644"},
			file{Path: sshdDropIn, Content: "AuthorizedKeysFile .ssh/authorized_keys /etc/ssh/vedutaos_authorized_keys/%u\n", Permissions: "0644"},
		)
	}
	// The dashboard owns the first terminal: no login prompt may come back to it, not even
	// from Raspberry Pi OS's first-user service, which re-enables it when it finishes.
	cc.Runcmd = append(cc.Runcmd,
		"systemctl mask getty@tty1.service",
		"systemctl daemon-reload",
		"systemctl enable "+UnitName,
	)
	if len(o.SSHKeys) > 0 {
		cc.Runcmd = append(cc.Runcmd, "systemctl enable ssh.service", "systemctl reload-or-restart ssh.service || true")
	}
	switch o.Target {
	case Pi:
		// The panel's driver looks for its firmware once, at boot, and the firmware was
		// only just installed.
		cc.PowerState = &powerState{Mode: "reboot", Message: "VedutaOS is installed; restarting", Condition: true}
	case QEMU:
		cc.Runcmd = append(cc.Runcmd, "systemctl start --no-block "+UnitName)
	}

	var b bytes.Buffer
	b.WriteString("#cloud-config\n" + marker + "\n")
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cc); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// MetaData returns cloud-init's meta-data. The instance id is a digest of what the first
// boot acts on, so a card written with different settings is set up again when it next
// starts, and an unchanged one is not.
func MetaData(acted ...[]byte) []byte {
	h := sha256.New()
	for _, b := range acted {
		fmt.Fprintf(h, "%d:", len(b))
		h.Write(b)
	}
	id := hex.EncodeToString(h.Sum(nil))[:16]
	return []byte(marker + "\ndsmode: local\ninstance-id: vedutaos-" + id + "\n")
}

// UserConf returns Raspberry Pi OS's userconf.txt: the first user becomes veduta, with no
// password that can be typed. Without it the image asks for a user on the first boot and
// waits for an answer that a console with a gamepad cannot give.
func UserConf() []byte {
	return []byte(User + ":*\n")
}

// vtconsoles runs a shell loop over the kernel's text consoles drawn on a framebuffer.
// Dollars are doubled for systemd.
func vtconsoles(bind int) string {
	return `/bin/sh -c 'for c in /sys/class/vtconsole/vtcon*; do grep -q "frame buffer" "$$c/name" && echo ` +
		strconv.Itoa(bind) + ` > "$$c/bind"; done'`
}

// Unit returns the systemd unit that runs the dashboard.
func Unit() []byte {
	return []byte(`[Unit]
Description=VedutaOS dashboard
Documentation=https://github.com/riftbane/vedutaos
After=systemd-user-sessions.service
RequiresMountsFor=` + Mount + `
Conflicts=getty@tty1.service

[Service]
EnvironmentFile=-` + Mount + "/" + EnvFile + `
# The text console would draw over the frames: take the framebuffer from it while the
# dashboard runs, and give it back when it stops.
ExecStartPre=-` + vtconsoles(0) + `
ExecStart=` + Mount + "/" + VShell + `
ExecStopPost=-` + vtconsoles(1) + `
StandardInput=tty
StandardOutput=journal
StandardError=journal
TTYPath=/dev/tty1
TTYReset=yes
TTYVHangup=yes
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
`)
}

// Env returns the dashboard's settings file, in systemd's EnvironmentFile form. Games
// launched by the dashboard inherit it.
func Env(o Options) []byte {
	var b strings.Builder
	b.WriteString("# VedutaOS settings, read each time the dashboard starts.\n" + marker + "\n")
	b.WriteString("VEDUTA_BACKEND=fbdev\n")
	fmt.Fprintf(&b, "VEDUTA_SCALE=%d\n", o.Scale)
	b.WriteString("VEDUTAOS_GAMES=" + Mount + "/" + Games + "\n")
	if o.FB != "" {
		b.WriteString("VEDUTA_FB=" + o.FB + "\n")
	}
	if o.Pad != "" {
		b.WriteString("VEDUTA_PAD=" + o.Pad + "\n")
	}
	return []byte(b.String())
}

const (
	blockBegin = "# vedutaos begin: the console's panel. " + "Written by vedutaos card, replaced each time it runs."
	blockEnd   = "# vedutaos end"
)

// ConfigTxt returns a Raspberry Pi config.txt with the console's block in it: the panel's
// overlay when o has a panel, nothing otherwise. Everything else in the file is kept as it
// was, so running it again, or with other settings, changes only the block.
func ConfigTxt(existing []byte, o Options) []byte {
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
	if !o.Panel {
		return []byte(out)
	}
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	gpios := "dc-gpio=" + strconv.Itoa(o.Pins.DC)
	if o.Pins.Reset >= 0 {
		gpios = "reset-gpio=" + strconv.Itoa(o.Pins.Reset) + "," + gpios
	}
	if o.Pins.Backlight >= 0 {
		gpios += ",backlight-gpio=" + strconv.Itoa(o.Pins.Backlight)
	}
	return []byte(out + blockBegin + "\n" +
		"[all]\n" +
		"dtparam=spi=on\n" +
		"dtoverlay=mipi-dbi-spi,spi0-0,speed=" + strconv.Itoa(o.SPISpeed) + ",write-only\n" +
		"dtparam=compatible=" + panel.Compatible + `\0panel-mipi-dbi-spi` + "\n" +
		"dtparam=width=320,height=240\n" +
		"dtparam=" + gpios + "\n" +
		blockEnd + "\n")
}

// cmdlineSettings are the kernel settings a console needs: no blinking cursor on the text
// console, and no blanking it after ten minutes without a key.
var cmdlineSettings = []string{"vt.global_cursor_default=0", "consoleblank=0"}

// Cmdline returns a Raspberry Pi cmdline.txt with the console's kernel settings, replacing
// any earlier value of the same settings and keeping everything else.
func Cmdline(existing []byte) ([]byte, error) {
	text := strings.TrimRight(string(existing), "\r\n")
	if text == "" || strings.ContainsAny(text, "\r\n") {
		return nil, errors.New("provision: cmdline.txt must hold exactly one line")
	}
	var out []string
	for _, f := range strings.Fields(text) {
		key, _, _ := strings.Cut(f, "=")
		drop := false
		for _, s := range cmdlineSettings {
			if k, _, _ := strings.Cut(s, "="); k == key {
				drop = true
			}
		}
		if !drop {
			out = append(out, f)
		}
	}
	out = append(out, cmdlineSettings...)
	return []byte(strings.Join(out, " ") + "\n"), nil
}

// Replaceable reports whether a user-data file can be overwritten without losing anything:
// it is missing or empty, was written here, or holds nothing but comments, as the one
// Raspberry Pi OS ships does. One written by Raspberry Pi Imager's customisation is not.
func Replaceable(userData []byte) bool {
	for _, l := range strings.Split(string(userData), "\n") {
		t := strings.TrimSpace(l)
		if t == marker {
			return true
		}
		if t != "" && !strings.HasPrefix(t, "#") {
			return false
		}
	}
	return true
}
