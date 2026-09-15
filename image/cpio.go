package image

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// cpio writes an archive in the "newc" format, the one the kernel unpacks an initramfs
// from: for each entry an ASCII header, the name, then the data, each padded to four
// bytes, and a TRAILER!!! entry at the end.
type cpio struct {
	w   io.Writer
	n   int64
	ino uint32
	err error
}

func newCpio(w io.Writer) *cpio { return &cpio{w: w, ino: 1} }

// Unix file types, as the mode field carries them.
const (
	modeDir  = 0o040000
	modeFile = 0o100000
	modeLink = 0o120000
)

func (c *cpio) dir(name string) error                         { return c.add(name, modeDir|0o755, nil) }
func (c *cpio) file(name string, perm uint32, b []byte) error { return c.add(name, modeFile|perm, b) }
func (c *cpio) symlink(name, target string) error             { return c.add(name, modeLink|0o777, []byte(target)) }

func (c *cpio) add(name string, mode uint32, data []byte) error {
	if c.err != nil {
		return c.err
	}
	name = strings.TrimPrefix(name, "/")
	if name == "" || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") {
		return fmt.Errorf("cpio: bad entry name %q", name)
	}
	nlink := 1
	if mode&modeDir != 0 {
		nlink = 2
	}
	hdr := fmt.Sprintf("070701%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x%08x",
		c.ino, mode, 0, 0, nlink, 0, len(data), 0, 0, 0, 0, len(name)+1, 0)
	c.ino++
	c.write([]byte(hdr))
	c.write([]byte(name))
	c.write([]byte{0})
	c.pad()
	c.write(data)
	c.pad()
	return c.err
}

// close writes the trailer. The archive is complete only once it has been called.
func (c *cpio) close() error {
	if c.err != nil {
		return c.err
	}
	c.add("TRAILER!!!", 0, nil)
	return c.err
}

func (c *cpio) write(b []byte) {
	if c.err != nil {
		return
	}
	n, err := c.w.Write(b)
	c.n += int64(n)
	c.err = err
}

func (c *cpio) pad() {
	if r := c.n % 4; r != 0 {
		c.write(make([]byte, 4-r))
	}
}

// cpioEntry is one entry read back from an archive, for the tests and the build's own
// check of what it wrote.
type cpioEntry struct {
	Name string
	Mode uint32
	Data []byte
}

// readCpio parses a newc archive.
func readCpio(r io.Reader) ([]cpioEntry, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var out []cpioEntry
	pos := 0
	for {
		if len(b)-pos < 110 {
			return nil, errors.New("cpio: archive ends inside a header")
		}
		h := b[pos : pos+110]
		if string(h[:6]) != "070701" {
			return nil, fmt.Errorf("cpio: bad magic %q at %d", h[:6], pos)
		}
		field := func(i int) (int, error) {
			v, err := strconv.ParseUint(string(h[6+8*i:14+8*i]), 16, 32)
			return int(v), err
		}
		mode, err := field(1)
		if err != nil {
			return nil, err
		}
		size, err := field(6)
		if err != nil {
			return nil, err
		}
		nameLen, err := field(11)
		if err != nil {
			return nil, err
		}
		pos += 110
		if len(b)-pos < nameLen {
			return nil, errors.New("cpio: archive ends inside a name")
		}
		name := string(bytes.TrimRight(b[pos:pos+nameLen], "\x00"))
		pos = align4(pos + nameLen)
		if name == "TRAILER!!!" {
			return out, nil
		}
		if len(b)-pos < size {
			return nil, fmt.Errorf("cpio: archive ends inside %s", name)
		}
		out = append(out, cpioEntry{Name: name, Mode: uint32(mode), Data: b[pos : pos+size]})
		pos = align4(pos + size)
	}
}

func align4(n int) int { return (n + 3) &^ 3 }
