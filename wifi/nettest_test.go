package wifi

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestDefaultGateway(t *testing.T) {
	const routes = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"wlan0\t0008090A\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n" +
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
		"wlan0\t00000000\t0108090A\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"
	if gw, err := DefaultGateway(routes, "wlan0"); err != nil || gw != "10.9.8.1" {
		t.Errorf("wlan0: %s %v", gw, err)
	}
	if gw, err := DefaultGateway(routes, ""); err != nil || gw != "192.168.1.1" {
		t.Errorf("any: %s %v", gw, err)
	}
	if _, err := DefaultGateway(routes, "wlan1"); err == nil {
		t.Error("a device with no default route has a router")
	}
}

func TestParsePing(t *testing.T) {
	const ok = "PING 1.1.1.1 (1.1.1.1): 56 data bytes\n64 bytes from 1.1.1.1: seq=0 ttl=57 time=12.123 ms\n\n" +
		"--- 1.1.1.1 ping statistics ---\n3 packets transmitted, 2 packets received, 33% packet loss\n" +
		"round-trip min/avg/max = 11.100/12.600/14.000 ms\n"
	r, found := ParsePing(ok)
	if !found || r.Sent != 3 || r.Received != 2 || r.Average != 12600*time.Microsecond {
		t.Errorf("%+v %v", r, found)
	}
	r, found = ParsePing("PING 10.0.0.1 (10.0.0.1): 56 data bytes\n\n--- 10.0.0.1 ping statistics ---\n3 packets transmitted, 0 packets received, 100% packet loss\n")
	if !found || r.Received != 0 {
		t.Errorf("no answer: %+v %v", r, found)
	}
	if _, found := ParsePing("ping: bad address 'x'"); found {
		t.Error("an error read as a result")
	}
}

func waitTest(t *testing.T, tr *Tester) TestStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for tr.Status().Running {
		if time.Now().After(deadline) {
			t.Fatal("the test did not end")
		}
		time.Sleep(time.Millisecond)
	}
	return tr.Status()
}

func TestTester(t *testing.T) {
	pinged := []string{}
	o := TestOptions{
		Connected: func() bool { return true },
		Gateway:   func() (string, error) { return "192.168.1.1", nil },
		Ping: func(host string) (PingResult, error) {
			pinged = append(pinged, host)
			if host == InternetHost {
				return PingResult{Sent: 3}, nil // no answer
			}
			return PingResult{Sent: 3, Received: 3, Average: 4 * time.Millisecond}, nil
		},
		Resolve: func(string) ([]string, error) {
			return nil, &net.DNSError{Err: "server misbehaving", Name: NameToLookUp}
		},
	}
	var tr Tester
	tr.Start(o)
	st := waitTest(t, &tr)
	want := []Check{
		{Name: "ROUTER", State: CheckOK, Result: "192.168.1.1 4 ms, 3/3 replies"},
		{Name: "INTERNET", State: CheckFailed, Result: "1.1.1.1: no answer"},
		{Name: "DNS", State: CheckFailed, Result: "github.com: server misbehaving"},
	}
	for i, c := range want {
		if st.Checks[i] != c {
			t.Errorf("check %d: %+v, want %+v", i, st.Checks[i], c)
		}
	}
	if len(pinged) != 2 {
		t.Errorf("pinged %v", pinged)
	}

	// Not connected: nothing is tried.
	o.Connected = func() bool { return false }
	o.Ping = func(string) (PingResult, error) { t.Error("pinged while not connected"); return PingResult{}, nil }
	tr.Start(o)
	for _, c := range waitTest(t, &tr).Checks {
		if c.State != CheckFailed || c.Result != "not connected" {
			t.Errorf("%+v", c)
		}
	}

	// No router: said, and the rest still tried.
	o.Connected = func() bool { return true }
	o.Gateway = func() (string, error) { return "", errors.New("no router") }
	o.Ping = func(string) (PingResult, error) { return PingResult{Sent: 3, Received: 3}, nil }
	o.Resolve = func(string) ([]string, error) { return []string{"140.82.121.4"}, nil }
	tr.Start(o)
	st = waitTest(t, &tr)
	if st.Checks[0].Result != "no router" || st.Checks[1].State != CheckOK || st.Checks[2].State != CheckOK {
		t.Errorf("%+v", st.Checks)
	}
}
