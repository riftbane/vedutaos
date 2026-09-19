// Package clock finds out what time it is from the network. A board without a clock
// battery starts in 1970, and HTTPS refuses every certificate until the clock is right.
//
// It asks NTP servers first (SNTP, RFC 4330: one UDP packet each way), and when none
// answers — some networks let no NTP out — reads the Date of a plain HTTP answer. Names are
// looked up by Go's resolver: busybox, static, can look up none.
package clock

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// Servers are the NTP servers asked, in turn.
var Servers = []string{"pool.ntp.org", "time.cloudflare.com", "time.google.com"}

// HTTPURL is a plain HTTP page whose answer's Date is read when no NTP server answers.
const HTTPURL = "http://connectivitycheck.gstatic.com/generate_204"

// ntpEpoch is NTP's time zero, 1900-01-01.
var ntpEpoch = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)

// Query asks one NTP server (host or host:port) for the time.
func Query(ctx context.Context, server string) (time.Time, error) {
	if _, _, err := net.SplitHostPort(server); err != nil {
		server = net.JoinHostPort(server, "123")
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", server)
	if err != nil {
		return time.Time{}, err
	}
	defer conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	conn.SetDeadline(deadline)
	req := make([]byte, 48)
	req[0] = 0<<6 | 4<<3 | 3 // no leap warning, version 4, client
	sent := time.Now()
	if _, err := conn.Write(req); err != nil {
		return time.Time{}, err
	}
	resp := make([]byte, 48)
	n, err := conn.Read(resp)
	if err != nil {
		return time.Time{}, err
	}
	rtt := time.Since(sent)
	if n < 48 {
		return time.Time{}, errors.New("a short answer")
	}
	if mode := resp[0] & 7; mode != 4 {
		return time.Time{}, fmt.Errorf("an answer in mode %d, not a server's", mode)
	}
	if stratum := resp[1]; stratum == 0 || stratum > 15 {
		return time.Time{}, errors.New("the server does not know the time")
	}
	secs := binary.BigEndian.Uint32(resp[40:])
	frac := binary.BigEndian.Uint32(resp[44:])
	if secs == 0 {
		return time.Time{}, errors.New("an answer with no time")
	}
	t := ntpEpoch.Add(time.Duration(secs) * time.Second).Add(time.Duration(uint64(frac) * uint64(time.Second) >> 32))
	return t.Add(rtt / 2), nil
}

// FromHTTP reads the Date of a plain HTTP answer.
func FromHTTP(ctx context.Context, c *http.Client, url string) (time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return time.Time{}, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	resp.Body.Close()
	date := resp.Header.Get("Date")
	if date == "" {
		return time.Time{}, errors.New("the answer has no date")
	}
	return http.ParseTime(date)
}

// Now asks the servers in turn, then the HTTP page, and says where the time came from.
func Now(ctx context.Context, servers []string, httpURL string) (time.Time, string, error) {
	var why []string
	for _, s := range servers {
		qctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		t, err := Query(qctx, s)
		cancel()
		if err == nil {
			return t, s, nil
		}
		why = append(why, s+": "+short(err))
	}
	if httpURL != "" {
		hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		t, err := FromHTTP(hctx, &http.Client{}, httpURL)
		if err == nil {
			return t, httpURL, nil
		}
		why = append(why, "http: "+short(err))
	}
	return time.Time{}, "", errors.New(strings.Join(why, "; "))
}

// short is the end of an error, which says what went wrong without where.
func short(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 {
		s = s[i+2:]
	}
	return s
}
