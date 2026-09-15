package image

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCpioRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	c := newCpio(&buf)
	c.dir("/bin")
	c.file("/bin/hello", 0o755, []byte("#!/bin/sh\necho hi\n"))
	c.file("etc/empty", 0o644, nil)
	c.symlink("/bin/sh", "busybox")
	if err := c.close(); err != nil {
		t.Fatal(err)
	}
	got, err := readCpio(&buf)
	if err != nil {
		t.Fatal(err)
	}
	want := []cpioEntry{
		{"bin", modeDir | 0o755, nil},
		{"bin/hello", modeFile | 0o755, []byte("#!/bin/sh\necho hi\n")},
		{"etc/empty", modeFile | 0o644, nil},
		{"bin/sh", modeLink | 0o777, []byte("busybox")},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i].Name || got[i].Mode != want[i].Mode || !bytes.Equal(got[i].Data, want[i].Data) {
			t.Errorf("entry %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	if buf.Len()%4 != 0 {
		t.Errorf("archive length %d is not a multiple of four", buf.Len())
	}
}

// The kernel's reader is not available in a test, but the cpio program reads the same
// format: it must list what was written.
func TestCpioReadByTheTool(t *testing.T) {
	cpioTool, err := exec.LookPath("cpio")
	if err != nil {
		t.Skip("no cpio program")
	}
	var buf bytes.Buffer
	c := newCpio(&buf)
	c.dir("lib")
	c.dir("lib/modules")
	c.file("lib/modules/order", 0o644, []byte("a\nb\n"))
	c.file("init", 0o755, bytes.Repeat([]byte{1}, 1001))
	if err := c.close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cmd := exec.Command(cpioTool, "-idm", "--quiet")
	cmd.Dir = dir
	cmd.Stdin = &buf
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cpio: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "lib", "modules", "order"))
	if err != nil || string(b) != "a\nb\n" {
		t.Errorf("order: %q %v", b, err)
	}
	fi, err := os.Stat(filepath.Join(dir, "init"))
	if err != nil || fi.Size() != 1001 || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("init: %v %v", fi, err)
	}
}

func TestCpioRefusesEscapes(t *testing.T) {
	c := newCpio(&bytes.Buffer{})
	for _, bad := range []string{"", "/", "../x", "a/../../b"} {
		if err := c.add(bad, modeFile, nil); err == nil || !strings.Contains(err.Error(), "bad entry") {
			t.Errorf("%q accepted", bad)
		}
		c.err = nil
	}
}
