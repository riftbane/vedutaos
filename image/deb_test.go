package image

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnpackDeb(t *testing.T) {
	deb := fakeDeb(map[string][]byte{"usr/bin/busybox": []byte("bb"), "usr/share/doc/": nil, "etc/x.conf": []byte("x")})
	path := filepath.Join(t.TempDir(), "a.deb")
	os.WriteFile(path, deb, 0o644)
	dst := filepath.Join(t.TempDir(), "pkg")
	if err := unpackDeb(path, dst); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "usr", "bin", "busybox")); err != nil || string(b) != "bb" {
		t.Errorf("busybox: %q %v", b, err)
	}
	if fi, err := os.Stat(filepath.Join(dst, "usr", "bin", "busybox")); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Error("busybox is not executable")
	}
	if fi, err := os.Stat(filepath.Join(dst, "usr", "share", "doc")); err != nil || !fi.IsDir() {
		t.Error("the directory entry was not made")
	}
	// A second run finds the stamp and does nothing; a changed .deb unpacks again.
	os.WriteFile(filepath.Join(dst, "marker"), nil, 0o644)
	if err := unpackDeb(path, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "marker")); err != nil {
		t.Error("an unpacked package was unpacked again")
	}
	os.WriteFile(path, fakeDeb(map[string][]byte{"etc/y": nil}), 0o644)
	if err := unpackDeb(path, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "marker")); err == nil {
		t.Error("a changed package was not unpacked afresh")
	}
}

func TestUntarRefusesEscapes(t *testing.T) {
	deb := fakeDeb(map[string][]byte{"../escape": []byte("x")})
	path := filepath.Join(t.TempDir(), "bad.deb")
	os.WriteFile(path, deb, 0o644)
	if err := unpackDeb(path, filepath.Join(t.TempDir(), "pkg")); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("%v", err)
	}
}

func TestFetchPackage(t *testing.T) {
	deb := fakeDeb(map[string][]byte{"a": []byte("a")})
	sum := sumOf(deb)
	served := map[string][]byte{"http://x/pool/a_1.deb": deb}
	calls := 0
	get := func(url string) (io.ReadCloser, error) {
		calls++
		b, ok := served[url]
		if !ok {
			return nil, os.ErrNotExist
		}
		return io.NopCloser(bytes.NewReader(b)), nil
	}
	dir := t.TempDir()
	p := Package{Name: "a", Version: "1", URLs: []string{"http://gone/a_1.deb", "http://x/pool/a_1.deb"}, SHA256: sum}
	got, err := fetchPackage(p, dir, get, io.Discard)
	if err != nil || filepath.Base(got) != "a_1.deb" {
		t.Fatalf("%q %v", got, err)
	}
	if calls != 2 {
		t.Errorf("the second URL should have been tried after the first failed: %d calls", calls)
	}
	if _, err := fetchPackage(p, dir, get, io.Discard); err != nil || calls != 2 {
		t.Errorf("a cached package was fetched again: %v, %d calls", err, calls)
	}
	p.SHA256 = strings.Repeat("0", 64)
	if _, err := fetchPackage(p, t.TempDir(), get, io.Discard); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("a wrong checksum was accepted: %v", err)
	}
}

func sumOf(b []byte) string {
	f, _ := os.CreateTemp("", "sum")
	f.Write(b)
	f.Close()
	defer os.Remove(f.Name())
	s, _ := fileSum(f.Name())
	return s
}
