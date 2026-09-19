//go:build linux

package wifi

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// ctrl is a connection to wpa_supplicant's control socket: a datagram socket of our own,
// bound to a path of its own, connected to the one wpa_supplicant listens on for an
// interface. A request is one datagram and its answer one datagram; an attached connection
// also receives the events, which start with "<level>".
type ctrl struct {
	conn  *net.UnixConn
	local string
}

var ctrlSeq atomic.Int32

// dialCtrl connects to the socket of an interface under dir.
func dialCtrl(dir, iface string) (*ctrl, error) {
	local := filepath.Join(os.TempDir(), fmt.Sprintf("wpa_ctrl_%d-%d", os.Getpid(), ctrlSeq.Add(1)))
	os.Remove(local)
	conn, err := net.DialUnix("unixgram", &net.UnixAddr{Name: local, Net: "unixgram"}, &net.UnixAddr{Name: filepath.Join(dir, iface), Net: "unixgram"})
	if err != nil {
		return nil, err
	}
	return &ctrl{conn: conn, local: local}, nil
}

func (c *ctrl) close() {
	c.conn.Close()
	os.Remove(c.local)
}

// request sends a command and returns its answer, skipping any event that arrives first.
func (c *ctrl) request(cmd string) (string, error) {
	c.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.conn.Write([]byte(cmd)); err != nil {
		return "", err
	}
	buf := make([]byte, 16<<10)
	for {
		n, err := c.conn.Read(buf)
		if err != nil {
			return "", err
		}
		s := string(buf[:n])
		if strings.HasPrefix(s, "<") {
			continue
		}
		return s, nil
	}
}

// ok sends a command that answers OK.
func (c *ctrl) ok(cmd string) error {
	s, err := c.request(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(s) != "OK" {
		return fmt.Errorf("wpa_supplicant: %s: %s", strings.Fields(cmd)[0], strings.TrimSpace(s))
	}
	return nil
}

// event waits up to d for the next event, and returns it without its level.
func (c *ctrl) event(d time.Duration) (string, error) {
	c.conn.SetDeadline(time.Now().Add(d))
	buf := make([]byte, 4<<10)
	n, err := c.conn.Read(buf)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return "", nil
		}
		return "", err
	}
	s := string(buf[:n])
	if strings.HasPrefix(s, "<") {
		if i := strings.IndexByte(s, '>'); i >= 0 {
			s = s[i+1:]
		}
	}
	return s, nil
}
