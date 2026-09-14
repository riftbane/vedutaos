package provision

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update-golden", false, "rewrite the reference files")

const testKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGVkdXRhb3MgdGVzdCBrZXkgZm9yIGdvbGRlbnM veduta@test"

func piPanel() Options {
	return Options{Target: Pi, Panel: true, Pins: DefaultPins, SPISpeed: DefaultSPISpeed, Scale: 1, SSHKeys: []string{testKey}}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update-golden once the file is right)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs (run with -update-golden once it is right):\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

func stock(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "stock", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Every file a card carries is pinned, for a Raspberry Pi with a panel, one on HDMI, and the
// emulator. The stock files are Raspberry Pi OS's own, from pi-gen.
func TestGolden(t *testing.T) {
	hdmi := Options{Target: Pi, Scale: 1}
	qemu := Options{Target: QEMU, Scale: 4}
	for name, o := range map[string]Options{"pi-panel": piPanel(), "pi-hdmi": hdmi, "qemu": qemu} {
		ud, err := UserData(o)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		golden(t, name+"/"+UserDataFile, ud)
		golden(t, name+"/env", Env(o))
		if o.Target == Pi {
			golden(t, name+"/"+ConfigFile, ConfigTxt(stock(t, ConfigFile), o))
			cl, err := Cmdline(stock(t, CmdlineFile))
			if err != nil {
				t.Fatal(err)
			}
			golden(t, name+"/"+CmdlineFile, cl)
		}
	}
	golden(t, UnitName, Unit())
}

// user-data must be YAML that cloud-init reads. It is the "#cloud-config" line, a comment,
// and a JSON document — and JSON is YAML.
func TestUserDataIsJSONAfterItsHeader(t *testing.T) {
	for _, o := range []Options{piPanel(), {Target: QEMU, Scale: 4}} {
		ud, err := UserData(o)
		if err != nil {
			t.Fatal(err)
		}
		first, rest, _ := strings.Cut(string(ud), "\n")
		if first != "#cloud-config" {
			t.Fatalf("first line %q", first)
		}
		_, body, _ := strings.Cut(rest, "\n")
		var doc map[string]any
		dec := json.NewDecoder(strings.NewReader(body))
		if err := dec.Decode(&doc); err != nil {
			t.Fatalf("%s: body is not JSON: %v", o.Target, err)
		}
		if dec.More() {
			t.Fatalf("%s: more than one document", o.Target)
		}
		if strings.Contains(body, "\t") {
			t.Fatalf("%s: a tab, which YAML refuses outside quotes", o.Target)
		}
	}
}

func TestOnlyThePiIsRestartedAndOnlyThePanelGetsFirmware(t *testing.T) {
	pi, _ := UserData(piPanel())
	hdmi, _ := UserData(Options{Target: Pi, Scale: 1})
	qemu, _ := UserData(Options{Target: QEMU, Scale: 4})
	if !bytes.Contains(pi, []byte(`"mode": "reboot"`)) || bytes.Contains(qemu, []byte("power_state")) {
		t.Error("the Pi restarts after setup so the panel finds its firmware; the emulator starts the dashboard at once")
	}
	if !bytes.Contains(pi, []byte("/lib/firmware/vedutaos-ili9341.bin")) || bytes.Contains(hdmi, []byte("/lib/firmware")) {
		t.Error("firmware is installed exactly when there is a panel")
	}
	if !bytes.Contains(qemu, []byte("LABEL=CIDATA /boot/firmware")) || bytes.Contains(pi, []byte("CIDATA")) {
		t.Error("only the emulator mounts its card disk")
	}
	if bytes.Contains(hdmi, []byte("authorized_keys")) || bytes.Contains(hdmi, []byte("enable ssh")) {
		t.Error("ssh is set up without a key")
	}
	if !bytes.Contains(pi, []byte("authorized_keys")) || !bytes.Contains(pi, []byte("enable ssh")) {
		t.Error("a key was given and ssh is not set up")
	}
}

// systemd expands $name in commands; the unit's shell loops need their dollars doubled.
func TestUnitDollarsAreDoubled(t *testing.T) {
	u := string(Unit())
	for i := 0; i < len(u); i++ {
		if u[i] != '$' {
			continue
		}
		if i+1 >= len(u) || u[i+1] != '$' {
			t.Fatalf("single $ at %d: %q", i, u[max(0, i-20):min(len(u), i+20)])
		}
		i++
	}
}

func TestConfigTxtChangesOnlyItsBlock(t *testing.T) {
	orig := stock(t, ConfigFile)
	once := ConfigTxt(orig, piPanel())
	if !bytes.HasPrefix(once, orig) {
		t.Fatal("the rest of config.txt was not kept as it was")
	}
	if twice := ConfigTxt(once, piPanel()); !bytes.Equal(twice, once) {
		t.Fatalf("second run changed the file:\n%s", twice)
	}
	other := piPanel()
	other.SPISpeed = 16000000
	other.Pins = Pins{DC: 22, Reset: -1, Backlight: -1}
	changed := ConfigTxt(once, other)
	if bytes.Count(changed, []byte("# vedutaos begin")) != 1 || !bytes.Contains(changed, []byte("speed=16000000")) {
		t.Fatalf("block not replaced:\n%s", changed)
	}
	if !bytes.Contains(changed, []byte("dtparam=dc-gpio=22\n")) {
		t.Fatalf("unconnected lines should be left out:\n%s", changed)
	}
	if back := ConfigTxt(changed, Options{Target: Pi, Scale: 1}); !bytes.Equal(back, orig) {
		t.Fatalf("taking the panel away did not give back the stock file:\n%s", back)
	}
}

func TestConfigTxtWithoutTrailingNewline(t *testing.T) {
	got := ConfigTxt([]byte("dtparam=audio=on"), piPanel())
	if !bytes.HasPrefix(got, []byte("dtparam=audio=on\n# vedutaos begin")) {
		t.Fatalf("%s", got)
	}
}

func TestCmdline(t *testing.T) {
	orig := stock(t, CmdlineFile)
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

func TestReplaceable(t *testing.T) {
	ours, _ := UserData(piPanel())
	for name, tc := range map[string]struct {
		data []byte
		want bool
	}{
		"missing":       {nil, true},
		"stock":         {stock(t, UserDataFile), true},
		"ours":          {ours, true},
		"imager":        {[]byte("#cloud-config\nhostname: raspberrypi\nusers:\n- name: nikita\n"), false},
		"only a header": {[]byte("#cloud-config\n"), true},
	} {
		if got := Replaceable(tc.data); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

func TestMetaDataFollowsWhatTheFirstBootDoes(t *testing.T) {
	a := MetaData([]byte("user-data"), []byte("firmware"))
	if !bytes.Equal(a, MetaData([]byte("user-data"), []byte("firmware"))) {
		t.Error("the same card gave two instance ids")
	}
	if bytes.Equal(a, MetaData([]byte("user-data"), []byte("other firmware"))) {
		t.Error("a changed firmware would not be installed again")
	}
	if bytes.Equal(MetaData([]byte("ab"), []byte("c")), MetaData([]byte("a"), []byte("bc"))) {
		t.Error("parts are not kept apart")
	}
	if !bytes.Contains(a, []byte("\ndsmode: local\ninstance-id: vedutaos-")) {
		t.Errorf("%s", a)
	}
}

func TestValidate(t *testing.T) {
	bad := map[string]Options{
		"target":     {Target: "pc", Scale: 1},
		"scale":      {Target: QEMU, Scale: 9},
		"qemu panel": {Target: QEMU, Panel: true, Scale: 1},
		"dc":         {Target: Pi, Panel: true, Pins: Pins{DC: -1}, SPISpeed: 1, Scale: 1},
		"speed":      {Target: Pi, Panel: true, Pins: DefaultPins, Scale: 1},
		"fb":         {Target: Pi, Scale: 1, FB: "fb1\nVEDUTA_SCALE=8"},
		"key":        {Target: Pi, Scale: 1, SSHKeys: []string{"-----BEGIN OPENSSH PRIVATE KEY-----"}},
	}
	for name, o := range bad {
		if err := o.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := piPanel().Validate(); err != nil {
		t.Error(err)
	}
}

func TestUserConfNamesTheUserWithoutAPassword(t *testing.T) {
	if got := string(UserConf()); got != "veduta:*\n" {
		t.Fatalf("%q", got)
	}
}
