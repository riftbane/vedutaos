package wifi

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestKey: the key is what wpa_passphrase computes, checked against the examples of IEEE
// 802.11i, annex H.4.
func TestKey(t *testing.T) {
	for _, c := range []struct{ ssid, pass, want string }{
		{"IEEE", "password", "f42c6fc52df0ebef9ebb4b90b38a5f902e83fe1b135a70e23aed762e9710a12e"},
		{"ThisIsASSID", "ThisIsAPassword", "0dc0d6eb90555ed6419756b9a15ec3e3209b63df707dd508d14581f8982721af"},
	} {
		if got := Key(c.ssid, c.pass); got != c.want {
			t.Errorf("Key(%q, %q) = %s, want %s", c.ssid, c.pass, got, c.want)
		}
	}
}

func TestPassphraseOK(t *testing.T) {
	for p, want := range map[string]bool{"": false, "1234567": false, "12345678": true, "with space ok": true,
		string(make([]byte, 64)): false, "tab\there!": false, "àccentato": false} {
		if got := PassphraseOK(p); got != want {
			t.Errorf("PassphraseOK(%q) = %v", p, got)
		}
	}
}

func TestParseScanResults(t *testing.T) {
	const r = "bssid / frequency / signal level / flags / ssid\n" +
		"aa:bb:cc:00:00:01\t2412\t-70\t[WPA2-PSK-CCMP][ESS]\tHome\n" +
		"aa:bb:cc:00:00:02\t5180\t-50\t[WPA2-PSK-CCMP][ESS]\tHome\n" + // the same network, stronger
		"aa:bb:cc:00:00:03\t2437\t-60\t[ESS]\tCafe \\\"Free\\\"\n" +
		"aa:bb:cc:00:00:04\t2462\t-80\t[WPA2-EAP-CCMP][ESS]\tOffice\n" +
		"aa:bb:cc:00:00:05\t2462\t-40\t[WPA2-PSK-CCMP][ESS]\t\n" + // hidden
		"aa:bb:cc:00:00:06\t2412\t-85\t[RSN-SAE-CCMP][ESS]\tNew\n" +
		"aa:bb:cc:00:00:07\t2412\t-65\t[WPA2-PSK+SAE-CCMP][ESS]\tMixed\\xc3\\xa8\n"
	want := []Network{
		{SSID: "Home", Signal: -50, Security: PSK},
		{SSID: `Cafe "Free"`, Signal: -60, Security: Open},
		{SSID: "Mixedè", Signal: -65, Security: PSK},
		{SSID: "Office", Signal: -80, Security: Unsupported},
		{SSID: "New", Signal: -85, Security: Unsupported},
	}
	if got := ParseScanResults(r); !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestBars(t *testing.T) {
	for signal, want := range map[int]int{-40: 4, -60: 3, -70: 2, -90: 1} {
		if got := (Network{Signal: signal}).Bars(); got != want {
			t.Errorf("%d dBm: %d bars, want %d", signal, got, want)
		}
	}
}

func TestParseStatus(t *testing.T) {
	st := ParseStatus("bssid=aa:bb:cc:00:00:01\nssid=Home\nwpa_state=COMPLETED\nip_address=192.168.1.9\n")
	if st["wpa_state"] != "COMPLETED" || st["ssid"] != "Home" {
		t.Fatalf("%v", st)
	}
}

// TestStore: networks are remembered most recent first, a name is kept once, and a
// damaged file or a bad key is nothing rather than an error.
func TestStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vedutaos", "wifi.json")
	s := &Store{Path: path, Write: path}
	k := Key("Home", "password")
	for _, n := range []Saved{{SSID: "Home", Key: k}, {SSID: "Cafe"}, {SSID: "Home", Key: k}} {
		if err := s.Remember(n); err != nil {
			t.Fatal(err)
		}
	}
	again := &Store{Path: path}
	again.Load()
	if want := []Saved{{SSID: "Home", Key: k}, {SSID: "Cafe"}}; !reflect.DeepEqual(again.Networks, want) {
		t.Fatalf("loaded %+v, want %+v", again.Networks, want)
	}
	if _, ok := again.Find("Cafe"); !ok {
		t.Fatal("Cafe not found")
	}
	if err := again.Forget("Cafe"); err == nil {
		t.Fatal("a store with nowhere to write wrote")
	}
	if err := s.Forget("Cafe"); err != nil {
		t.Fatal(err)
	}
	s.Load()
	if len(s.Networks) != 1 || s.Networks[0].SSID != "Home" {
		t.Fatalf("after Forget: %+v", s.Networks)
	}
	os.WriteFile(path, []byte(`{"networks":[{"ssid":"x","key":"nothex"},{"ssid":"y"}]`), 0o600)
	s.Load()
	if s.Networks != nil {
		t.Fatalf("a damaged file gave %+v", s.Networks)
	}
	os.WriteFile(path, []byte(`{"networks":[{"ssid":"x","key":"nothex"},{"ssid":"y"}]}`), 0o600)
	s.Load()
	if want := []Saved{{SSID: "y"}}; !reflect.DeepEqual(s.Networks, want) {
		t.Fatalf("a bad key: %+v", s.Networks)
	}
}

func TestDriverMessage(t *testing.T) {
	log := "<6>[    1.0] mmc1: new high speed SDIO card at address 0001\n" +
		"<6>[    5.1] brcmfmac: F1 signature read @0x18000000=0x15264345\n" +
		"<3>[    6.2] brcmfmac: brcmf_sdio_htclk: HT Avail timeout (1000000): clkctl 0x50\n" +
		"<6>[    7.0] usb 1-1: new device\n"
	if got := DriverMessage(log, true); got != "brcmfmac: brcmf_sdio_htclk: HT Avail timeout (1000000): clkctl 0x50" {
		t.Errorf("%q", got)
	}
	if got := DriverMessage("", true); got != "the Wi-Fi driver found no Wi-Fi chip" {
		t.Errorf("%q", got)
	}
	if got := DriverMessage(log, false); !strings.Contains(got, "not loaded") {
		t.Errorf("%q", got)
	}
}
