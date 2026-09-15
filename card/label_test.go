package card

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// mtools writes the label into the boot sector and the root directory, like Linux does.
func TestReadLabelFromMtools(t *testing.T) {
	if _, err := exec.LookPath("mformat"); err != nil {
		t.Skip("no mtools")
	}
	for _, c := range []struct {
		name string
		args []string
	}{
		{"fat32", []string{"-F", "-T", "131072", "-h", "16", "-s", "32"}},
		{"fat16", []string{"-T", "65536", "-h", "16", "-s", "32"}},
	} {
		img := filepath.Join(t.TempDir(), c.name+".img")
		f, _ := os.Create(img)
		f.Truncate(64 << 20)
		f.Close()
		cmd := exec.Command("mformat", append(append([]string{"-i", img}, c.args...), "-v", "VEDUTA", "::")...)
		cmd.Env = append(os.Environ(), "MTOOLS_SKIP_CHECK=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", c.name, err, out)
		}
		f, err := os.Open(img)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ReadLabel(f)
		f.Close()
		if err != nil || got != "VEDUTA" {
			t.Errorf("%s: %q %v", c.name, got, err)
		}
	}
}

// A volume formatted by Windows has its label only in the root directory; one with no
// root entry has it only in the boot sector.
func TestReadLabelPlaces(t *testing.T) {
	fat32 := func(bootLabel, rootLabel string) []byte {
		img := make([]byte, 64<<10)
		bs := img[:512]
		bs[0] = 0xeb
		binary.LittleEndian.PutUint16(bs[11:], 512)
		bs[13] = 1 // sectors per cluster
		binary.LittleEndian.PutUint16(bs[14:], 1)
		bs[16] = 1
		binary.LittleEndian.PutUint32(bs[36:], 2) // sectors per FAT
		binary.LittleEndian.PutUint32(bs[44:], 2) // root cluster
		bs[66] = 0x29
		copy(bs[71:82], []byte("NO NAME    "))
		if bootLabel != "" {
			copy(bs[71:82], []byte(bootLabel + "           ")[:11])
		}
		bs[510], bs[511] = 0x55, 0xaa
		if rootLabel != "" {
			root := img[(1+2)*512:]
			copy(root[:11], []byte(rootLabel + "           ")[:11])
			root[11] = 0x08
		}
		return img
	}
	for _, c := range []struct {
		boot, root, want string
	}{
		{"", "VEDUTA", "VEDUTA"},
		{"VEDUTAOS", "", "VEDUTAOS"},
		{"OLD", "VEDUTA", "VEDUTA"},
		{"", "", ""},
	} {
		got, err := ReadLabel(bytes.NewReader(fat32(c.boot, c.root)))
		if err != nil || got != c.want {
			t.Errorf("boot %q root %q: %q %v", c.boot, c.root, got, err)
		}
	}
	if _, err := ReadLabel(bytes.NewReader(make([]byte, 4096))); err == nil {
		t.Error("zeros were taken for a FAT file system")
	}
	mbr := make([]byte, 512)
	mbr[510], mbr[511] = 0x55, 0xaa
	if _, err := ReadLabel(bytes.NewReader(mbr)); err == nil {
		t.Error("a partition table was taken for a FAT file system")
	}
}
