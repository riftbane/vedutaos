package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.5.0-rc.10", "v0.5.0-rc.9", true}, // numbers compare as numbers
		{"v0.5.0-rc.9", "v0.5.0-rc.10", false},
		{"v0.5.0", "v0.5.0-rc.10", true}, // a release is later than its pre-releases
		{"v0.5.0-rc.1", "v0.4.0", true},
		{"v0.4.0", "v0.5.0-rc.9", false},
		{"v1.0.0", "v1.0.0", false},
		{"v0.10.0", "v0.9.9", true},
		{"v0.5.0-rc.9", "dev", true}, // a development build takes any release
		{"dev", "v0.1.0", false},
		{"v1.0.0-beta", "v1.0.0-alpha.2", true},
		{"v1.0.0-rc.1.1", "v1.0.0-rc.1", true},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

func TestChannel(t *testing.T) {
	if DefaultChannel("v0.5.0-rc.9") != Beta || DefaultChannel("v1.0.0") != Stable {
		t.Fatal("the default channel is not the version's")
	}
	file := filepath.Join(t.TempDir(), "vedutaos", "channel")
	if got := LoadChannel(file, "v1.0.0"); got != Stable {
		t.Fatalf("no file: %s", got)
	}
	if err := SaveChannel(file, Beta); err != nil {
		t.Fatal(err)
	}
	if got := LoadChannel(file, "v1.0.0"); got != Beta {
		t.Fatalf("saved beta, loaded %s", got)
	}
	os.WriteFile(file, []byte("nightly\n"), 0o644)
	if got := LoadChannel(file, "v0.5.0-rc.1"); got != Beta {
		t.Fatalf("an unknown channel: %s", got)
	}
}

// bootArchive makes an archive of boot files.
func bootArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))})
		tw.Write([]byte(content))
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

// server serves GitHub's list of releases and the releases' files. Each release is a tag,
// whether it is a pre-release, and its boot archive (nil: the release has none).
type release struct {
	tag   string
	pre   bool
	draft bool
	boot  []byte
}

func server(t *testing.T, releases []release) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/repos/"+Repo+"/releases", func(w http.ResponseWriter, r *http.Request) {
		type asset struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}
		var list []map[string]any
		for _, rel := range releases {
			assets := []asset{{"vedutaos.img.xz", srv.URL + "/dl/" + rel.tag + "/vedutaos.img.xz"}}
			if rel.boot != nil {
				assets = append(assets, asset{BootArchive, srv.URL + "/dl/" + rel.tag + "/" + BootArchive}, asset{Checksums, srv.URL + "/dl/" + rel.tag + "/" + Checksums})
			}
			list = append(list, map[string]any{"tag_name": rel.tag, "prerelease": rel.pre, "draft": rel.draft, "assets": assets})
		}
		json.NewEncoder(w).Encode(list)
	})
	for _, rel := range releases {
		if rel.boot == nil {
			continue
		}
		sum := sha256.Sum256(rel.boot)
		boot, sums := rel.boot, fmt.Sprintf("%s  vedutaos.img.xz\n%s  %s\n", strings.Repeat("0", 64), hex.EncodeToString(sum[:]), BootArchive)
		mux.HandleFunc("/dl/"+rel.tag+"/"+BootArchive, func(w http.ResponseWriter, r *http.Request) { w.Write(boot) })
		mux.HandleFunc("/dl/"+rel.tag+"/"+Checksums, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sums)) })
	}
	return srv
}

// TestLatest: stable takes releases, beta pre-releases too; drafts and releases without
// boot files are passed over; the newest wins whatever GitHub's order.
func TestLatest(t *testing.T) {
	boot := bootArchive(t, map[string]string{"kernel8.img": "k"})
	srv := server(t, []release{
		{tag: "v0.5.0-rc.10", pre: true, boot: boot},
		{tag: "v0.6.0-rc.1", pre: true, draft: true, boot: boot},
		{tag: "v0.5.0-rc.11", pre: true}, // no boot files
		{tag: "v0.4.1", boot: boot},
		{tag: "v0.4.0"},
		{tag: "v0.4.2-rc.1", pre: true, boot: boot},
	})
	for ch, want := range map[Channel]string{Beta: "v0.5.0-rc.10", Stable: "v0.4.1"} {
		r, err := Latest(context.Background(), srv.Client(), srv.URL, ch)
		if err != nil || r.Tag != want {
			t.Errorf("%s: %s %v, want %s", ch, r.Tag, err, want)
		}
	}
	empty := server(t, []release{{tag: "v0.4.0"}})
	if _, err := Latest(context.Background(), empty.Client(), empty.URL, Beta); err == nil {
		t.Error("a list with no boot files gave a release")
	}
}

func TestFetchChecks(t *testing.T) {
	boot := bootArchive(t, map[string]string{"kernel8.img": "k"})
	srv := server(t, []release{{tag: "v1.0.0", boot: boot}})
	r, err := Latest(context.Background(), srv.Client(), srv.URL, Stable)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "update.tar.gz")
	var last int64
	if err := Fetch(context.Background(), srv.Client(), r, dst, func(done, total int64) { last = done }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); !bytes.Equal(b, boot) || last != int64(len(boot)) {
		t.Fatalf("fetched %d bytes, progress %d, want %d", len(b), last, len(boot))
	}
	// A damaged download is refused and left nowhere.
	bad := server(t, []release{{tag: "v1.0.0", boot: boot}})
	r2, _ := Latest(context.Background(), bad.Client(), bad.URL, Stable)
	r2.Assets[BootArchive] = srv.URL + "/dl/v1.0.0/" + Checksums // other bytes
	dst2 := filepath.Join(t.TempDir(), "update.tar.gz")
	if err := Fetch(context.Background(), bad.Client(), r2, dst2, nil); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("a damaged archive: %v", err)
	}
	if _, err := os.Stat(dst2); err == nil {
		t.Fatal("the damaged archive was kept")
	}
	if _, err := os.Stat(dst2 + ".part"); err == nil {
		t.Fatal("the damaged download was kept")
	}
}

func writeArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), BootArchive)
	os.WriteFile(p, bootArchive(t, files), 0o644)
	return p
}

// TestInstall replaces the boot files and adds new ones, keeps the wiring and everything
// the archive does not hold, and leaves no half-written file behind.
func TestInstall(t *testing.T) {
	card := t.TempDir()
	for name, content := range map[string]string{
		"kernel8.img": "old kernel", "config.txt": "my panel", "overlays/vedutaos-buttons.dtbo": "my buttons",
		"games/cube/main.lua": "game", "saves/cube/s.json": "save", "vedutaos/wifi.json": "wifi",
	} {
		os.MkdirAll(filepath.Dir(filepath.Join(card, name)), 0o755)
		os.WriteFile(filepath.Join(card, name), []byte(content), 0o644)
	}
	archive := writeArchive(t, map[string]string{
		"kernel8.img": "new kernel", "overlays/new.dtbo": "new overlay", "config.txt": "stock panel",
		"overlays/vedutaos-buttons.dtbo": "stock buttons", "cmdline.txt": "console=tty1", "vedutaos/release": "VedutaOS v2",
	})
	n, err := Install(archive, card)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("installed %d files, want 4", n)
	}
	for name, want := range map[string]string{
		"kernel8.img": "new kernel", "overlays/new.dtbo": "new overlay", "config.txt": "my panel",
		"overlays/vedutaos-buttons.dtbo": "my buttons", "cmdline.txt": "console=tty1", // a card without one gets it
		"vedutaos/release": "VedutaOS v2", "games/cube/main.lua": "game", "saves/cube/s.json": "save", "vedutaos/wifi.json": "wifi",
	} {
		if b, err := os.ReadFile(filepath.Join(card, name)); err != nil || string(b) != want {
			t.Errorf("%s: %q %v, want %q", name, b, err, want)
		}
	}
	filepath.Walk(card, func(p string, fi os.FileInfo, err error) error {
		if strings.HasSuffix(p, newSuffix) {
			t.Errorf("%s left behind", p)
		}
		return nil
	})
}

// TestInstallRefusesAndUndoes: an archive naming a file outside the card, or a damaged
// one, changes nothing.
func TestInstallRefusesAndUndoes(t *testing.T) {
	card := t.TempDir()
	os.WriteFile(filepath.Join(card, "kernel8.img"), []byte("old"), 0o644)
	for _, archive := range []string{
		writeArchive(t, map[string]string{"kernel8.img": "new", "../outside": "x"}),
		func() string {
			p := filepath.Join(t.TempDir(), BootArchive)
			b := bootArchive(t, map[string]string{"kernel8.img": strings.Repeat("new", 10000)})
			os.WriteFile(p, b[:len(b)/2], 0o644) // cut short
			return p
		}(),
	} {
		if _, err := Install(archive, card); err == nil {
			t.Error("installed from a bad archive")
		}
		if b, _ := os.ReadFile(filepath.Join(card, "kernel8.img")); string(b) != "old" {
			t.Errorf("kernel8.img is %q after a refused install", b[:min(len(b), 10)])
		}
		entries, _ := os.ReadDir(card)
		if len(entries) != 1 {
			t.Errorf("the card holds %d entries, want the kernel alone", len(entries))
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(card), "outside")); err == nil {
			t.Error("a file was written outside the card")
		}
	}
}

// TestUpdater runs a whole update: newer on the channel, downloaded, installed, restart.
func TestUpdater(t *testing.T) {
	boot := bootArchive(t, map[string]string{"kernel8.img": "new kernel"})
	srv := server(t, []release{{tag: "v0.5.0-rc.10", pre: true, boot: boot}})
	card := t.TempDir()
	os.WriteFile(filepath.Join(card, "kernel8.img"), []byte("old kernel"), 0o644)
	synced := false
	o := Options{Client: srv.Client(), API: srv.URL, Channel: Beta, Current: "v0.5.0-rc.9", Card: card,
		Archive: filepath.Join(card, "vedutaos", "update.tar.gz"), Sync: func() { synced = true }}
	var u Updater
	u.Start(o)
	st := wait(t, &u, Restart)
	if st.Version != "v0.5.0-rc.10" || st.Done != int64(len(boot)) {
		t.Fatalf("status %+v", st)
	}
	if b, _ := os.ReadFile(filepath.Join(card, "kernel8.img")); string(b) != "new kernel" || !synced {
		t.Fatalf("kernel %q synced %v", b, synced)
	}
	if _, err := os.Stat(o.Archive); err == nil {
		t.Error("the download was left on the card")
	}

	// On stable, or already on the latest, there is nothing to do.
	for _, o2 := range []Options{{Channel: Stable, Current: "v0.5.0-rc.9"}, {Channel: Beta, Current: "v0.5.0-rc.10"}} {
		o2.Client, o2.API, o2.Card, o2.Archive = srv.Client(), srv.URL, card, o.Archive
		var u2 Updater
		u2.Start(o2)
		if o2.Channel == Stable {
			wait(t, &u2, Failed) // no stable release carries boot files
		} else {
			wait(t, &u2, UpToDate)
		}
	}
	// Not ready: the reason is shown.
	var u3 Updater
	o.Ready = func() error { return fmt.Errorf("connect to wi-fi first") }
	u3.Start(o)
	if st := wait(t, &u3, Failed); st.Err != "connect to wi-fi first" {
		t.Fatalf("not ready: %+v", st)
	}
}

func wait(t *testing.T, u *Updater, want State) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := u.Status()
		if st.State == want {
			return st
		}
		if st.State == Failed || st.State == UpToDate || st.State == Restart || time.Now().After(deadline) {
			t.Fatalf("status %+v, waiting for %d", st, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
