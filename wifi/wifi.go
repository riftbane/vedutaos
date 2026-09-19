// Package wifi joins the console to a wireless network: the networks in range, the one it
// is on, and the ones it has joined before, which it joins again by itself.
//
// The radio is driven by wpa_supplicant, which the image carries with its libraries, through
// its control socket; busybox's udhcpc asks for an address once a network is joined. This
// file is the part that needs neither: what a network is, what the console is doing, how a
// passphrase becomes a key, how wpa_supplicant's answers read, and the file on the card
// that remembers networks. The rest (manager_linux.go) runs only on the console.
package wifi

import (
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Security is how a network is protected, as far as the console is concerned.
type Security uint8

const (
	Open        Security = iota // no password
	PSK                         // a WPA or WPA2 passphrase (WPA3 networks that also take WPA2)
	Unsupported                 // enterprise (802.1X), WEP, or WPA3 alone
)

// Network is one network in range, by its name.
type Network struct {
	SSID     string
	Signal   int // dBm, the strongest of its access points
	Security Security
	Saved    bool // joined before: joining again needs no password
}

// Bars is the signal as the dashboard draws it, 1 to 4.
func (n Network) Bars() int {
	switch {
	case n.Signal >= -55:
		return 4
	case n.Signal >= -67:
		return 3
	case n.Signal >= -78:
		return 2
	}
	return 1
}

// State is what the console's Wi-Fi is doing.
type State uint8

const (
	NoAdapter     State = iota // no wireless device, or none yet
	Starting                   // the device is there and wpa_supplicant is starting
	Disconnected               // on no network
	Connecting                 // joining SSID
	Connected                  // on SSID, with an address once IP is set
	WrongPassword              // SSID refused the password
	Failed                     // SSID could not be joined, for another reason
)

// Status is what the dashboard shows of the Wi-Fi.
type Status struct {
	State    State
	SSID     string    // the network being joined or joined
	IP       string    // the console's address on it, once it has one
	Networks []Network // in range, strongest first
	Scanning bool
	Detail   string // with no adapter, what the driver said about it
}

// DriverMessage is why there is no wireless device, from the kernel's messages: the
// driver's last word about it, or that the driver is not loaded (loaded says whether it
// is), or that it found no chip.
func DriverMessage(klog string, loaded bool) string {
	if !loaded {
		return "the Wi-Fi driver (brcmfmac) is not loaded"
	}
	last := ""
	for _, l := range strings.Split(klog, "\n") {
		if strings.Contains(l, "brcmf") {
			last = l
		}
	}
	if last == "" {
		return "the Wi-Fi driver found no Wi-Fi chip"
	}
	// "<3>[    5.123456] brcmfmac: ...": the level and the time say nothing to a player.
	if strings.HasPrefix(last, "<") {
		if i := strings.IndexByte(last, '>'); i >= 0 {
			last = last[i+1:]
		}
	}
	if strings.HasPrefix(last, "[") {
		if i := strings.IndexByte(last, ']'); i >= 0 {
			last = last[i+1:]
		}
	}
	return strings.TrimSpace(last)
}

// PassphraseOK reports whether a WPA passphrase can be used: 8 to 63 printable ASCII
// characters, as the standard says.
func PassphraseOK(p string) bool {
	if len(p) < 8 || len(p) > 63 {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] < 32 || p[i] > 126 {
			return false
		}
	}
	return true
}

// Key is the network key a WPA passphrase makes for a network, in hex: PBKDF2-SHA1 of the
// passphrase salted with the network's name, 4096 rounds, 32 bytes, as wpa_passphrase
// computes it. The card keeps the key rather than the passphrase.
func Key(ssid, passphrase string) string {
	k, err := pbkdf2.Key(sha1.New, passphrase, []byte(ssid), 4096, 32)
	if err != nil {
		panic(err) // only for lengths no caller asks for
	}
	return hex.EncodeToString(k)
}

// ParseScanResults reads wpa_supplicant's SCAN_RESULTS: a header, then one access point a
// line, "bssid<TAB>frequency<TAB>signal<TAB>flags<TAB>ssid". Access points of one network
// become one Network with the strongest signal; hidden networks (no name) are left out.
func ParseScanResults(s string) []Network {
	best := map[string]Network{}
	for i, line := range strings.Split(s, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if i == 0 || len(f) < 5 {
			continue
		}
		ssid := Unescape(f[4])
		if ssid == "" || strings.Trim(ssid, "\x00") == "" {
			continue
		}
		signal, _ := strconv.Atoi(f[2])
		if old, ok := best[ssid]; ok && old.Signal >= signal {
			continue
		}
		best[ssid] = Network{SSID: ssid, Signal: signal, Security: security(f[3])}
	}
	out := make([]Network, 0, len(best))
	for _, n := range best {
		out = append(out, n)
	}
	sortNetworks(out)
	return out
}

func sortNetworks(ns []Network) {
	sort.Slice(ns, func(i, j int) bool {
		if ns[i].Signal != ns[j].Signal {
			return ns[i].Signal > ns[j].Signal
		}
		return ns[i].SSID < ns[j].SSID
	})
}

// security reads an access point's flags, such as [WPA2-PSK-CCMP][ESS] or
// [RSN-SAE-CCMP][ESS].
func security(flags string) Security {
	switch {
	case strings.Contains(flags, "PSK"):
		return PSK
	case strings.Contains(flags, "EAP"), strings.Contains(flags, "WEP"), strings.Contains(flags, "SAE"),
		strings.Contains(flags, "WPA"), strings.Contains(flags, "RSN"), strings.Contains(flags, "OWE"):
		return Unsupported
	}
	return Open
}

// Unescape decodes a name as wpa_supplicant prints it: \\, \", \e, \n, \r, \t and \xNN.
func Unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'e':
			b.WriteByte(0x1b)
		case 'x':
			if i+2 < len(s) {
				if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
					b.WriteByte(byte(v))
					i += 2
					continue
				}
			}
			b.WriteString(`\x`)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// ParseStatus reads wpa_supplicant's STATUS, "key=value" lines, into a map.
func ParseStatus(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), "="); ok {
			m[k] = v
		}
	}
	return m
}

// Saved is a network the console joined, as the card keeps it.
type Saved struct {
	SSID string `json:"ssid"`
	Key  string `json:"key,omitempty"` // Key(SSID, passphrase); empty for an open network
}

// Store is the card's file of networks, most recently joined first.
type Store struct {
	Path     string // where it is read
	Write    string // where it is written: the same file on the card's writable mount; empty when the card cannot be written
	Networks []Saved
}

// Load reads the file. A missing or damaged file is no networks: the console asks again.
func (s *Store) Load() {
	s.Networks = nil
	b, err := os.ReadFile(s.Path)
	if err != nil {
		return
	}
	var v struct {
		Networks []Saved `json:"networks"`
	}
	if json.Unmarshal(b, &v) != nil {
		return
	}
	for _, n := range v.Networks {
		if n.SSID != "" && (n.Key == "" || validKey(n.Key)) {
			s.Networks = append(s.Networks, n)
		}
	}
}

func validKey(k string) bool {
	b, err := hex.DecodeString(k)
	return err == nil && len(b) == 32
}

// Find returns the saved network called ssid.
func (s *Store) Find(ssid string) (Saved, bool) {
	for _, n := range s.Networks {
		if n.SSID == ssid {
			return n, true
		}
	}
	return Saved{}, false
}

// Remember puts n first, replacing what was kept for its name, and writes the file.
func (s *Store) Remember(n Saved) error {
	out := []Saved{n}
	for _, o := range s.Networks {
		if o.SSID != n.SSID {
			out = append(out, o)
		}
	}
	s.Networks = out
	return s.save()
}

// Forget drops the network called ssid and writes the file.
func (s *Store) Forget(ssid string) error {
	var out []Saved
	for _, o := range s.Networks {
		if o.SSID != ssid {
			out = append(out, o)
		}
	}
	s.Networks = out
	return s.save()
}

func (s *Store) save() error {
	if s.Write == "" {
		return errors.New("wifi: the card cannot be written")
	}
	b, err := json.MarshalIndent(struct {
		Networks []Saved `json:"networks"`
	}{s.Networks}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Write), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.Write, append(b, '\n'), 0o600)
}
