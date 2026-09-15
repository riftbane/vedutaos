package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// releaseServer serves a release's image files under /<version>/<name>.
func releaseServer(t *testing.T, files map[string][]byte) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/v9.9.9/")
		if b, ok := files[name]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	saved, savedGet := releaseURL, httpGet
	t.Cleanup(func() { releaseURL, httpGet = saved, savedGet })
	releaseURL = func(version, name string) string { return srv.URL + "/" + version + "/" + name }
	httpGet = srv.Client().Get
}

func TestFetchRelease(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LocalAppData", cache)
	t.Setenv("HOME", cache)
	// The image is served unpacked under the .xz name and "xz" is a copy: the test is about
	// fetching and checking, not about xz.
	saved := lookPath
	t.Cleanup(func() { lookPath = saved })
	cat := filepath.Join(t.TempDir(), "xz")
	os.WriteFile(cat, []byte("#!/bin/sh\ncat \"$2\"\n"), 0o755)
	lookPath = func(name string) (string, error) { return cat, nil }

	img, kernel, initrd := []byte("raw image"), []byte("kernel"), []byte("initrd")
	files := map[string][]byte{"vedutaos.img.xz": img, "vmlinuz": kernel, "initrd.img": initrd}
	files["image-checksums.txt"] = []byte(sum(img) + "  vedutaos.img.xz\n" + sum(kernel) + "  vmlinuz\n" + sum(initrd) + "  initrd.img\n")
	releaseServer(t, files)

	var out strings.Builder
	got, err := fetchRelease("v9.9.9", &out)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(got); string(b) != "raw image" {
		t.Errorf("image: %q", b)
	}
	if !exists(filepath.Dir(got), "vmlinuz") || !exists(filepath.Dir(got), "initrd.img") {
		t.Error("kernel or initrd missing beside the image")
	}
	if strings.Count(out.String(), "fetching") != 3 {
		t.Errorf("output:\n%s", out.String())
	}
	out.Reset()
	if _, err := fetchRelease("v9.9.9", &out); err != nil || strings.Contains(out.String(), "fetching") {
		t.Errorf("fetched again: %v\n%s", err, out.String())
	}

	// A file that does not match its checksum is not kept.
	files["vmlinuz"] = []byte("other kernel")
	os.Remove(filepath.Join(filepath.Dir(got), "vmlinuz"))
	if _, err := fetchRelease("v9.9.9", &out); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("mismatch accepted: %v", err)
	}
	if exists(filepath.Dir(got), "vmlinuz") || exists(filepath.Dir(got), "vmlinuz.part") {
		t.Error("a file that did not match was kept")
	}
	if _, err := fetchRelease("dev", &out); err == nil {
		t.Error("a dev build fetched a release")
	}
}

func TestCheckDevice(t *testing.T) {
	withHost(t, "windows", "amd64")
	if err := checkDevice(`\\.\PhysicalDrive2`); err == nil || !strings.Contains(err.Error(), "Imager") {
		t.Errorf("windows: %v", err)
	}
	withHost(t, "linux", "amd64")
	if err := checkDevice(filepath.Join(t.TempDir(), "sdz")); err == nil {
		t.Error("a missing device was accepted")
	}
	plain := filepath.Join(t.TempDir(), "file")
	os.WriteFile(plain, nil, 0o644)
	if err := checkDevice(plain); err == nil || !strings.Contains(err.Error(), "not a device") {
		t.Errorf("a plain file: %v", err)
	}
}
