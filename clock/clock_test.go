package clock

import (
	"context"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ntpServer answers every request with the time given, as a stratum 2 server would, and
// returns its address.
func ntpServer(t *testing.T, now time.Time, stratum byte) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if n < 48 || buf[0]&7 != 3 {
				continue
			}
			resp := make([]byte, 48)
			resp[0] = 4<<3 | 4 // version 4, server
			resp[1] = stratum
			d := now.Sub(ntpEpoch)
			secs := uint32(d / time.Second)
			frac := uint32((uint64(d%time.Second) << 32) / uint64(time.Second))
			binary.BigEndian.PutUint32(resp[40:], secs)
			binary.BigEndian.PutUint32(resp[44:], frac)
			pc.WriteTo(resp, addr)
		}
	}()
	return pc.LocalAddr().String()
}

func TestQuery(t *testing.T) {
	want := time.Date(2026, 9, 19, 18, 30, 15, 250_000_000, time.UTC)
	got, err := Query(context.Background(), ntpServer(t, want, 2))
	if err != nil {
		t.Fatal(err)
	}
	if d := got.Sub(want); d < 0 || d > 100*time.Millisecond {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, err := Query(context.Background(), ntpServer(t, want, 0)); err == nil {
		t.Fatal("a server that does not know the time was believed")
	}
}

// TestNow: servers that do not answer are passed over; with none, the HTTP Date is read;
// with nothing, every reason is said.
func TestNow(t *testing.T) {
	want := time.Date(2026, 9, 19, 18, 30, 0, 0, time.UTC)
	silent, err := net.ListenPacket("udp", "127.0.0.1:0") // takes the request, never answers
	if err != nil {
		t.Skip(err)
	}
	defer silent.Close()
	good := ntpServer(t, want, 1)
	got, src, err := Now(context.Background(), []string{silent.LocalAddr().String(), good}, "")
	if err != nil || src != good || got.Sub(want) > 100*time.Millisecond {
		t.Fatalf("%v from %s: %v", got, src, err)
	}

	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", want.Format(http.TimeFormat))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer web.Close()
	got, src, err = Now(context.Background(), []string{silent.LocalAddr().String()}, web.URL)
	if err != nil || src != web.URL || !got.Equal(want) {
		t.Fatalf("%v from %s: %v", got, src, err)
	}

	if _, _, err := Now(context.Background(), []string{silent.LocalAddr().String()}, "http://127.0.0.1:1/"); err == nil {
		t.Fatal("a time from nowhere")
	}
}
