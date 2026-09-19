package wifi

import (
	"os/exec"
	"sync"
)

// Options say where the manager finds what it runs, and how it runs it.
type Options struct {
	SysNet     string   // /sys/class/net, where the wireless device is looked for
	Supplicant string   // the wpa_supplicant program
	Env        []string // its environment beyond the manager's (LD_LIBRARY_PATH)
	Busybox    string   // for udhcpc
	DHCPScript string   // what udhcpc runs with the address it got
	RunDir     string   // where wpa_supplicant's control sockets and its configuration go
	Store      *Store

	// Start and Wait run a program: init keeps track of what it started, so that its reaper
	// leaves those alone. exec.Cmd's own methods when nil.
	Start func(*exec.Cmd) error
	Wait  func(*exec.Cmd) error
	Say   func(format string, args ...any) // one line of status; nothing when nil
}

func (o *Options) defaults() {
	if o.Start == nil {
		o.Start = (*exec.Cmd).Start
	}
	if o.Wait == nil {
		o.Wait = (*exec.Cmd).Wait
	}
	if o.Say == nil {
		o.Say = func(string, ...any) {}
	}
	if o.Store == nil {
		o.Store = &Store{}
	}
}

// Manager runs the console's Wi-Fi in the background. Its methods never wait: a request is
// queued, and what comes of it shows in Status.
type Manager struct {
	o    Options
	mu   sync.Mutex
	st   Status
	reqs chan func(*session)
}

// Status is what the Wi-Fi is doing now: a copy, safe to keep.
func (m *Manager) Status() Status {
	if m == nil {
		return Status{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.st
	st.Networks = append([]Network(nil), m.st.Networks...)
	return st
}

func (m *Manager) update(f func(*Status)) {
	m.mu.Lock()
	f(&m.st)
	m.mu.Unlock()
}

// Scan asks for the networks in range.
func (m *Manager) Scan() { m.request(func(s *session) { s.scan() }) }

// Join joins a network: with a passphrase for a network that needs one, or with none for
// an open network or one joined before.
func (m *Manager) Join(ssid, passphrase string) {
	m.request(func(s *session) { s.join(ssid, passphrase) })
}

func (m *Manager) request(f func(*session)) {
	if m == nil || m.reqs == nil {
		return
	}
	select {
	case m.reqs <- f:
	default: // the manager is busy starting; the dashboard asks again
	}
}
