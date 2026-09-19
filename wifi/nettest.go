package wifi

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Wi-Fi test: whether the network the console is on reaches its router, the internet,
// and names, one after the other, each with a ping or a lookup.

// Hosts the test reaches.
const (
	InternetHost = "1.1.1.1"    // answers pings from anywhere
	NameToLookUp = "github.com" // where updates come from
)

// CheckState is where one check of the test is.
type CheckState uint8

const (
	CheckWaiting CheckState = iota
	CheckRunning
	CheckOK
	CheckFailed
)

// Check is one step of the test and what came of it.
type Check struct {
	Name   string // ROUTER, INTERNET, DNS
	State  CheckState
	Result string // "12 MS", "192.168.1.1: NO ANSWER"…
}

// TestStatus is what the dashboard shows of the test.
type TestStatus struct {
	Running bool
	Checks  []Check
}

// TestOptions are what the test reaches the network with.
type TestOptions struct {
	Connected func() bool                           // whether the Wi-Fi is on a network with an address
	Gateway   func() (string, error)                // the default route's router
	Ping      func(host string) (PingResult, error) // a few pings
	Resolve   func(name string) ([]string, error)
	Say       func(format string, args ...any)
}

// PingResult is what a few pings came to.
type PingResult struct {
	Sent, Received int
	Average        time.Duration
}

// Tester runs the test in the background, one run at a time.
type Tester struct {
	mu sync.Mutex
	st TestStatus
}

// Status is the test as it stands.
func (t *Tester) Status() TestStatus {
	if t == nil {
		return TestStatus{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.st
	st.Checks = append([]Check(nil), t.st.Checks...)
	return st
}

func (t *Tester) set(i int, c Check) {
	t.mu.Lock()
	t.st.Checks[i] = c
	t.mu.Unlock()
}

// Start runs the test, unless it is running.
func (t *Tester) Start(o TestOptions) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.st.Running {
		return
	}
	t.st = TestStatus{Running: true}
	for _, n := range checkNames {
		t.st.Checks = append(t.st.Checks, Check{Name: n})
	}
	go t.run(o)
}

// checkNames are the test's checks, in order.
var checkNames = []string{"ROUTER", "INTERNET", "DNS"}

func (t *Tester) run(o TestOptions) {
	if o.Say == nil {
		o.Say = func(string, ...any) {}
	}
	defer func() {
		t.mu.Lock()
		t.st.Running = false
		t.mu.Unlock()
	}()
	fail := func(i int, why string) {
		t.set(i, Check{Name: checkNames[i], State: CheckFailed, Result: why})
		o.Say("wifi test: %s: %s", strings.ToLower(checkNames[i]), why)
	}
	ok := func(i int, what string) {
		t.set(i, Check{Name: checkNames[i], State: CheckOK, Result: what})
		o.Say("wifi test: %s: ok, %s", strings.ToLower(checkNames[i]), what)
	}
	running := func(i int) {
		t.set(i, Check{Name: checkNames[i], State: CheckRunning})
	}
	if !o.Connected() {
		for i := range checkNames {
			fail(i, "not connected")
		}
		return
	}
	ping := func(i int, host string) {
		running(i)
		r, err := o.Ping(host)
		switch {
		case err != nil:
			fail(i, host+": "+err.Error())
		case r.Received == 0:
			fail(i, host+": no answer")
		default:
			ok(i, fmt.Sprintf("%s %d ms, %d/%d replies", host, r.Average.Round(time.Millisecond).Milliseconds(), r.Received, r.Sent))
		}
	}
	running(0)
	if gw, err := o.Gateway(); err != nil {
		fail(0, err.Error())
	} else {
		ping(0, gw)
	}
	ping(1, InternetHost)
	running(2)
	start := time.Now()
	if addrs, err := o.Resolve(NameToLookUp); err != nil || len(addrs) == 0 {
		why := "no answer"
		var de *net.DNSError
		switch {
		case errors.As(err, &de):
			why = de.Err
		case err != nil:
			why = err.Error()
		}
		fail(2, NameToLookUp+": "+why)
	} else {
		ok(2, fmt.Sprintf("%s %d ms", NameToLookUp, time.Since(start).Milliseconds()))
	}
}

// DefaultGateway reads the default route's router for a device from /proc/net/route, whose
// addresses are hexadecimal in the machine's byte order (little-endian on the boards).
func DefaultGateway(routes, iface string) (string, error) {
	for i, line := range strings.Split(routes, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 3 || (iface != "" && f[0] != iface) || f[1] != "00000000" {
			continue
		}
		v, err := strconv.ParseUint(f[2], 16, 32)
		if err != nil || v == 0 {
			continue
		}
		return net.IPv4(byte(v), byte(v>>8), byte(v>>16), byte(v>>24)).String(), nil
	}
	return "", fmt.Errorf("no router")
}

var (
	pingCounts  = regexp.MustCompile(`(\d+) packets transmitted, (\d+) (?:packets )?received`)
	pingAverage = regexp.MustCompile(`min/avg/max(?:/mdev)? = [\d.]+/([\d.]+)/`)
)

// ParsePing reads what busybox's ping printed.
func ParsePing(out string) (PingResult, bool) {
	m := pingCounts.FindStringSubmatch(out)
	if m == nil {
		return PingResult{}, false
	}
	var r PingResult
	r.Sent, _ = strconv.Atoi(m[1])
	r.Received, _ = strconv.Atoi(m[2])
	if a := pingAverage.FindStringSubmatch(out); a != nil {
		if ms, err := strconv.ParseFloat(a[1], 64); err == nil {
			r.Average = time.Duration(ms * float64(time.Millisecond))
		}
	}
	return r, true
}
