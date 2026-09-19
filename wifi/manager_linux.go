//go:build linux

package wifi

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// joinTimeout is how long a network is given to take the console before it counts as failed.
const joinTimeout = 30 * time.Second

// Start begins looking for the wireless device and, once there is one, runs wpa_supplicant
// and udhcpc on it and joins the networks the card remembers.
func Start(o Options) *Manager {
	o.defaults()
	m := &Manager{o: o, reqs: make(chan func(*session), 16)}
	go m.run()
	return m
}

// session is the manager's goroutine's own state while wpa_supplicant runs.
type session struct {
	m       *Manager
	iface   string
	c       *ctrl
	ids     map[string]string // network name → wpa_supplicant's id, for the saved networks
	joining *joining
	dhcp    *exec.Cmd
	inRange int    // networks in the last scan, said when it changes
	address string // the last address said
}

// joining is a Join under way.
type joining struct {
	ssid, id, key string
	fresh         bool // added for this Join, not a saved network
	deadline      time.Time
}

func (m *Manager) run() {
	for {
		iface := m.findDevice()
		m.o.Say("wifi: %s", iface)
		m.update(func(st *Status) { *st = Status{State: Starting} })
		err := m.serve(iface)
		m.o.Say("wifi: %v", err)
		m.update(func(st *Status) { *st = Status{State: NoAdapter} })
		time.Sleep(5 * time.Second)
	}
}

// findDevice waits for a wireless device: the first, by name, that has a wireless/ or
// phy80211 entry in sysfs.
func (m *Manager) findDevice() string {
	for {
		entries, _ := os.ReadDir(m.o.SysNet)
		var names []string
		for _, e := range entries {
			for _, sub := range []string{"wireless", "phy80211"} {
				if _, err := os.Stat(filepath.Join(m.o.SysNet, e.Name(), sub)); err == nil {
					names = append(names, e.Name())
					break
				}
			}
		}
		if len(names) > 0 {
			sort.Strings(names)
			return names[0]
		}
		time.Sleep(time.Second)
	}
}

// serve runs wpa_supplicant and udhcpc on the device until wpa_supplicant ends.
func (m *Manager) serve(iface string) error {
	if err := os.MkdirAll(m.o.RunDir, 0o755); err != nil {
		return err
	}
	sockets := filepath.Join(m.o.RunDir, "sockets")
	conf := filepath.Join(m.o.RunDir, "wpa_supplicant.conf")
	if err := os.WriteFile(conf, []byte("ctrl_interface="+sockets+"\nupdate_config=0\n"), 0o600); err != nil {
		return err
	}
	os.Remove(filepath.Join(sockets, iface))
	sup := exec.Command(m.o.Supplicant, "-i", iface, "-D", "nl80211", "-c", conf)
	sup.Env = append(os.Environ(), m.o.Env...)
	sup.Stdout, sup.Stderr = os.Stderr, os.Stderr
	if err := m.o.Start(sup); err != nil {
		return err
	}
	ended := make(chan error, 1)
	go func() { ended <- m.o.Wait(sup) }()
	defer func() {
		sup.Process.Kill()
	}()

	var c, events *ctrl
	for deadline := time.Now().Add(15 * time.Second); ; {
		var err error
		if c, err = dialCtrl(sockets, iface); err == nil {
			break
		}
		select {
		case err := <-ended:
			return fmt.Errorf("wpa_supplicant ended: %v", err)
		case <-time.After(200 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wpa_supplicant's socket: %v", err)
		}
	}
	defer c.close()
	events, err := dialCtrl(sockets, iface)
	if err != nil {
		return err
	}
	defer events.close()
	if err := events.ok("ATTACH"); err != nil {
		return err
	}
	evs := make(chan string, 32)
	go func() {
		for {
			e, err := events.event(time.Hour)
			if err != nil {
				close(evs)
				return
			}
			if e != "" {
				evs <- e
			}
		}
	}()

	s := &session{m: m, iface: iface, c: c, ids: map[string]string{}}
	s.startDHCP()
	defer s.stopDHCP()
	s.loadSaved()
	s.poll()
	s.scan()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case err := <-ended:
			return fmt.Errorf("wpa_supplicant ended: %v", err)
		case f := <-m.reqs:
			f(s)
		case e, ok := <-evs:
			if !ok {
				return fmt.Errorf("wpa_supplicant's events ended")
			}
			s.event(e)
		case <-tick.C:
			s.poll()
		}
	}
}

// loadSaved hands wpa_supplicant the card's networks, most recent first, all enabled: it
// joins whichever is in range by itself.
func (s *session) loadSaved() {
	s.m.o.Store.Load()
	n := len(s.m.o.Store.Networks)
	for i, sv := range s.m.o.Store.Networks {
		id, err := s.add(sv.SSID, sv.Key)
		if err != nil {
			s.m.o.Say("wifi: %s: %v", sv.SSID, err)
			continue
		}
		s.c.ok("SET_NETWORK " + id + " priority " + strconv.Itoa(n-i))
		s.c.ok("ENABLE_NETWORK " + id)
		s.ids[sv.SSID] = id
	}
	if n > 0 {
		s.m.o.Say("wifi: %d saved networks", n)
	}
}

// add configures a network, disabled, and returns its id. key is empty for an open network.
func (s *session) add(ssid, key string) (string, error) {
	id, err := s.c.request("ADD_NETWORK")
	if err != nil {
		return "", err
	}
	id = strings.TrimSpace(id)
	if _, err := strconv.Atoi(id); err != nil {
		return "", fmt.Errorf("ADD_NETWORK: %s", id)
	}
	cmds := []string{"ssid " + hex.EncodeToString([]byte(ssid))}
	if key == "" {
		cmds = append(cmds, "key_mgmt NONE")
	} else {
		cmds = append(cmds, "key_mgmt WPA-PSK WPA-PSK-SHA256", "psk "+key, "ieee80211w 1")
	}
	for _, c := range cmds {
		if err := s.c.ok("SET_NETWORK " + id + " " + c); err != nil {
			s.c.ok("REMOVE_NETWORK " + id)
			return "", err
		}
	}
	return id, nil
}

func (s *session) scan() {
	r, err := s.c.request("SCAN")
	if err == nil && (strings.TrimSpace(r) == "OK" || strings.Contains(r, "FAIL-BUSY")) {
		s.m.update(func(st *Status) { st.Scanning = true })
	}
}

func (s *session) results() {
	r, err := s.c.request("SCAN_RESULTS")
	if err != nil {
		return
	}
	nets := ParseScanResults(r)
	for i := range nets {
		_, nets[i].Saved = s.m.o.Store.Find(nets[i].SSID)
	}
	s.m.update(func(st *Status) { st.Networks, st.Scanning = nets, false })
	if len(nets) != s.inRange {
		s.inRange = len(nets)
		s.m.o.Say("wifi: %d networks in range", len(nets))
	}
}

// join starts joining a network. A saved network is joined with its key unless a new
// passphrase is given, which replaces it.
func (s *session) join(ssid, passphrase string) {
	s.finish() // a Join under way is given up for this one
	j := &joining{ssid: ssid, deadline: time.Now().Add(joinTimeout)}
	if id, ok := s.ids[ssid]; ok && passphrase == "" {
		j.id = id
		sv, _ := s.m.o.Store.Find(ssid)
		j.key = sv.Key
	} else {
		if passphrase != "" {
			j.key = Key(ssid, passphrase)
		}
		id, err := s.add(ssid, j.key)
		if err != nil {
			s.m.o.Say("wifi: %s: %v", ssid, err)
			s.m.update(func(st *Status) { st.State, st.SSID, st.IP = Failed, ssid, "" })
			return
		}
		j.id, j.fresh = id, true
	}
	if err := s.c.ok("SELECT_NETWORK " + j.id); err != nil {
		s.m.o.Say("wifi: %s: %v", ssid, err)
		s.m.update(func(st *Status) { st.State, st.SSID, st.IP = Failed, ssid, "" })
		s.drop(j)
		return
	}
	s.joining = j
	s.m.o.Say("wifi: joining %s", ssid)
	s.m.update(func(st *Status) { st.State, st.SSID, st.IP = Connecting, ssid, "" })
}

// drop removes a network added for a Join that did not work, and enables the saved ones
// again, which SELECT_NETWORK disabled.
func (s *session) drop(j *joining) {
	if j.fresh {
		s.c.ok("REMOVE_NETWORK " + j.id)
	}
	s.enableSaved()
}

func (s *session) enableSaved() {
	for _, id := range s.ids {
		s.c.ok("ENABLE_NETWORK " + id)
	}
}

// finish gives up a Join under way.
func (s *session) finish() {
	if s.joining != nil {
		s.drop(s.joining)
		s.joining = nil
	}
}

func (s *session) event(e string) {
	switch {
	case strings.HasPrefix(e, "CTRL-EVENT-SCAN-RESULTS"):
		s.results()
	case strings.HasPrefix(e, "CTRL-EVENT-SCAN-FAILED"):
		s.m.update(func(st *Status) { st.Scanning = false })
	case strings.HasPrefix(e, "CTRL-EVENT-CONNECTED"):
		s.connected()
	case strings.HasPrefix(e, "CTRL-EVENT-SSID-TEMP-DISABLED"):
		if j := s.joining; j != nil && strings.Contains(e, "id="+j.id+" ") && strings.Contains(e, "reason=WRONG_KEY") {
			s.m.o.Say("wifi: %s refused the password", j.ssid)
			if !j.fresh {
				// The network's password changed: the saved key is no use any more.
				s.m.o.Store.Forget(j.ssid)
				delete(s.ids, j.ssid)
				j.fresh = true
			}
			s.joining = nil
			s.drop(j)
			s.m.update(func(st *Status) { st.State, st.SSID, st.IP = WrongPassword, j.ssid, "" })
		}
	}
}

// connected is wpa_supplicant's word that the console is on a network: a Join under way
// has worked, and the network is remembered; either way udhcpc is asked for an address at
// once rather than at its next try.
func (s *session) connected() {
	st := ParseStatus(s.status())
	ssid := Unescape(st["ssid"])
	if j := s.joining; j != nil && ssid == j.ssid {
		s.joining = nil
		if err := s.m.o.Store.Remember(Saved{SSID: j.ssid, Key: j.key}); err != nil {
			s.m.o.Say("wifi: %s is not remembered: %v", j.ssid, err)
		}
		if j.fresh {
			if old, ok := s.ids[j.ssid]; ok && old != j.id {
				s.c.ok("REMOVE_NETWORK " + old)
			}
			s.ids[j.ssid] = j.id
		}
		s.enableSaved()
		s.results() // the network is now saved
	}
	s.m.o.Say("wifi: on %s", ssid)
	if s.dhcp != nil && s.dhcp.Process != nil {
		s.dhcp.Process.Signal(syscall.SIGUSR1)
	}
	s.poll()
}

func (s *session) status() string {
	r, _ := s.c.request("STATUS")
	return r
}

// poll brings Status up to date with wpa_supplicant and the device's address.
func (s *session) poll() {
	if j := s.joining; j != nil && time.Now().After(j.deadline) {
		s.m.o.Say("wifi: %s did not take the console", j.ssid)
		s.joining = nil
		s.drop(j)
		s.m.update(func(st *Status) { st.State, st.SSID, st.IP = Failed, j.ssid, "" })
		return
	}
	if s.joining != nil {
		return
	}
	st := ParseStatus(s.status())
	ip := address(s.iface)
	if ip != s.address {
		s.address = ip
		if ip != "" {
			s.m.o.Say("wifi: address %s", ip)
		}
	}
	s.m.update(func(v *Status) {
		switch {
		case st["wpa_state"] == "COMPLETED":
			v.State, v.SSID, v.IP = Connected, Unescape(st["ssid"]), ip
		case v.State == WrongPassword || v.State == Failed:
			// said until the player joins again
		default:
			v.State, v.SSID, v.IP = Disconnected, "", ""
		}
	})
}

// address is the device's IPv4 address, or empty.
func address(iface string) string {
	i, err := net.InterfaceByName(iface)
	if err != nil {
		return ""
	}
	addrs, _ := i.Addrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			return n.IP.String()
		}
	}
	return ""
}

// startDHCP runs udhcpc on the device for as long as wpa_supplicant runs: it asks for an
// address again and again until a network gives one, and renews it.
func (s *session) startDHCP() {
	cmd := exec.Command(s.m.o.Busybox, "udhcpc", "-i", s.iface, "-f", "-s", s.m.o.DHCPScript, "-t", "3", "-T", "2", "-A", "3", "-x", "hostname:vedutaos")
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := s.m.o.Start(cmd); err != nil {
		s.m.o.Say("wifi: udhcpc: %v", err)
		return
	}
	s.dhcp = cmd
	go s.m.o.Wait(cmd)
}

func (s *session) stopDHCP() {
	if s.dhcp != nil && s.dhcp.Process != nil {
		s.dhcp.Process.Kill()
	}
}
