package image

import (
	"io/fs"
	"os/exec"
	"strings"
	"testing"
)

// The overlay is what the image is, beyond stock Raspberry Pi OS: check it hangs together.
func TestOverlay(t *testing.T) {
	must := func(name string) string {
		b, err := Overlay.ReadFile("rootfs" + name)
		if err != nil {
			t.Fatalf("overlay lacks %s", name)
		}
		return string(b)
	}
	unit := must("/etc/systemd/system/vedutaos.service")
	for _, want := range []string{"ExecStart=/usr/lib/vedutaos/start", "EnvironmentFile=/etc/default/vedutaos", "EnvironmentFile=-/boot/firmware/vedutaos/env", "Wants=vedutaos-card.service"} {
		if !strings.Contains(unit, want) {
			t.Errorf("vedutaos.service lacks %q", want)
		}
	}
	card := must("/etc/systemd/system/vedutaos-card.service")
	if !strings.Contains(card, "Before=ssh.service vedutaos.service") {
		t.Error("vedutaos-card.service must run before ssh and the dashboard")
	}
	start := must("/usr/lib/vedutaos/start")
	if !strings.Contains(start, "/boot/firmware/vedutaos/vshell") || !strings.Contains(start, "/usr/lib/vedutaos/vshell") {
		t.Error("start must prefer the card's vshell and fall back to the image's")
	}
	must("/usr/lib/vedutaos/card")
	def := must("/etc/default/vedutaos")
	if !strings.Contains(def, "VEDUTAOS_GAMES=/boot/firmware/games") {
		t.Error("defaults must name the games folder on the card")
	}
	if conf := must("/etc/systemd/system.conf.d/vedutaos.conf"); !strings.Contains(conf, "DefaultDeviceTimeoutSec=600s") {
		t.Error("the emulated first boot needs a long device timeout")
	}
	mods := must("/etc/initramfs-tools/modules")
	if !strings.Contains(mods, "virtio_blk") {
		t.Error("the QEMU initramfs needs virtio_blk")
	}
	if _, err := fs.Stat(Overlay, "rootfs/usr/lib/vedutaos/vshell"); err == nil {
		t.Error("the dashboard is built, not committed: rootfs must not carry vshell")
	}
}

// Every shell script parses; sh -n needs no root and no image.
func TestScriptsParse(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	scripts := map[string][]byte{"chroot.sh": chrootScript}
	for _, name := range []string{"rootfs/usr/lib/vedutaos/card", "rootfs/usr/lib/vedutaos/start"} {
		b, err := Overlay.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		scripts[name] = b
	}
	for name, src := range scripts {
		cmd := exec.Command(sh, "-n")
		cmd.Stdin = strings.NewReader(string(src))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", name, err, out)
		}
	}
}

func TestStockPinned(t *testing.T) {
	if len(Stock.SHA256) != 64 || !strings.HasSuffix(Stock.URL, ".img.xz") {
		t.Errorf("Stock is not a pinned .img.xz: %+v", Stock)
	}
}
