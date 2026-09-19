// Package update brings a console up to the latest VedutaOS of its channel: it asks GitHub
// for the releases, picks the newest of the channel (stable: releases; beta: pre-releases
// too), and when it is newer than the console's, downloads its boot files, checks them
// against the release's checksums and puts them on the card. The console then restarts
// into them.
//
// A release carries the boot files as one archive (BootArchive), the files of the image's
// volume without the games. Installing writes every file beside the one it replaces first,
// and only when all are written renames them over the old ones, so a card that fills up or
// a download cut short leaves the console as it was. The files that hold how the console
// is wired (Keep) are never replaced, nor is anything outside the archive: the games, the
// saves, the card's settings.
package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Where releases are, and what a release carries for an update.
const (
	Repo        = "riftbane/vedutaos"
	API         = "https://api.github.com"
	BootArchive = "vedutaos-boot.tar.gz" // the boot files, the image's volume without its games
	Checksums   = "image-checksums.txt"  // sha256sum's lines over the image's files, the archive among them
)

// Keep are the files of the card an update does not replace when the card has them: the
// panel's and the buttons' wiring, which the image was built with and a person may have
// edited.
var Keep = []string{"config.txt", "cmdline.txt", "overlays/vedutaos-buttons.dtbo"}

// Channel is which releases a console takes.
type Channel string

const (
	Stable Channel = "stable" // releases only
	Beta   Channel = "beta"   // pre-releases too
)

// DefaultChannel is the channel of a console that never chose: the one its own version is
// on.
func DefaultChannel(version string) Channel {
	if strings.Contains(version, "-") {
		return Beta
	}
	return Stable
}

// LoadChannel reads the channel kept in a file, or the default for the version.
func LoadChannel(file, version string) Channel {
	b, err := os.ReadFile(file)
	if err == nil {
		switch c := Channel(strings.TrimSpace(string(b))); c {
		case Stable, Beta:
			return c
		}
	}
	return DefaultChannel(version)
}

// SaveChannel keeps the channel in a file.
func SaveChannel(file string, c Channel) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(string(c)+"\n"), 0o644)
}

// Release is a release that can update a console.
type Release struct {
	Tag    string
	Assets map[string]string // name → download URL
}

// Latest returns the newest release of the channel that carries the boot files. Releases
// made before updates existed carry none and are passed over.
func Latest(ctx context.Context, c *http.Client, api string, ch Channel) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", api+"/repos/"+Repo+"/releases?per_page=50", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var list []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return Release{}, fmt.Errorf("GitHub's list of releases: %w", err)
	}
	var best Release
	for _, r := range list {
		if r.Draft || (r.Prerelease && ch != Beta) || !valid(r.Tag) {
			continue
		}
		assets := map[string]string{}
		for _, a := range r.Assets {
			assets[a.Name] = a.URL
		}
		if assets[BootArchive] == "" || assets[Checksums] == "" {
			continue
		}
		if best.Tag == "" || Newer(r.Tag, best.Tag) {
			best = Release{Tag: r.Tag, Assets: assets}
		}
	}
	if best.Tag == "" {
		return Release{}, errors.New("no release of the channel can update a console yet")
	}
	return best, nil
}

// Newer reports whether version a is later than b, as semantic versions (v1.2.3-rc.4). A
// version that is not one (a development build) is older than every release.
func Newer(a, b string) bool {
	switch {
	case !valid(a):
		return false
	case !valid(b):
		return true
	}
	return compare(a, b) > 0
}

type version struct {
	nums [3]int
	pre  []string
}

func parse(v string) (version, bool) {
	var out version
	v, ok := strings.CutPrefix(v, "v")
	if !ok {
		return out, false
	}
	v, _, _ = strings.Cut(v, "+")
	core, pre, hasPre := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out.nums[i] = n
	}
	if hasPre {
		if pre == "" {
			return out, false
		}
		out.pre = strings.Split(pre, ".")
	}
	return out, true
}

func valid(v string) bool {
	_, ok := parse(v)
	return ok
}

func compare(a, b string) int {
	va, _ := parse(a)
	vb, _ := parse(b)
	for i := range va.nums {
		if va.nums[i] != vb.nums[i] {
			return cmpInt(va.nums[i], vb.nums[i])
		}
	}
	switch {
	case len(va.pre) == 0 && len(vb.pre) == 0:
		return 0
	case len(va.pre) == 0:
		return 1 // a release is later than its pre-releases
	case len(vb.pre) == 0:
		return -1
	}
	for i := 0; i < len(va.pre) && i < len(vb.pre); i++ {
		x, y := va.pre[i], vb.pre[i]
		nx, ex := strconv.Atoi(x)
		ny, ey := strconv.Atoi(y)
		switch {
		case ex == nil && ey == nil:
			if nx != ny {
				return cmpInt(nx, ny)
			}
		case ex == nil:
			return -1 // numbers come before words
		case ey == nil:
			return 1
		case x != y:
			return strings.Compare(x, y)
		}
	}
	return cmpInt(len(va.pre), len(vb.pre))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Fetch downloads a release's boot files to dst, and checks them against the release's
// checksums. progress is told how much has come, of how much (0 when unknown).
func Fetch(ctx context.Context, c *http.Client, r Release, dst string, progress func(done, total int64)) error {
	sums, err := get(ctx, c, r.Assets[Checksums], 1<<20)
	if err != nil {
		return fmt.Errorf("%s: %w", Checksums, err)
	}
	want := ""
	for _, l := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(l); len(f) == 2 && strings.TrimPrefix(f[1], "*") == BootArchive {
			want = f[0]
		}
	}
	if want == "" {
		return fmt.Errorf("%s does not name %s", Checksums, BootArchive)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", r.Assets[BootArchive], nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", BootArchive, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	part := dst + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	h := sha256.New()
	counted := &counter{r: resp.Body, total: resp.ContentLength, progress: progress}
	_, err = io.Copy(io.MultiWriter(f, h), counted)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(part)
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(part)
		return fmt.Errorf("%s is damaged (sha256 %s, want %s)", BootArchive, got, want)
	}
	return os.Rename(part, dst)
}

type counter struct {
	r        io.Reader
	done     int64
	total    int64
	progress func(done, total int64)
	last     time.Time
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if c.progress != nil && (time.Since(c.last) > 200*time.Millisecond || err != nil) {
		c.last = time.Now()
		c.progress(c.done, max(c.total, 0))
	}
	return n, err
}

func get(ctx context.Context, c *http.Client, url string, limit int64) ([]byte, error) {
	if url == "" {
		return nil, errors.New("not in the release")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// newSuffix marks a file written beside the one it replaces, until every file is written.
const newSuffix = ".new"

// Install puts the boot files of an archive on the card under root, and returns how many
// files it replaced or added. It writes them all beside the files they replace, then
// renames them over; a failure before the renames leaves the card as it was.
func Install(archive, root string) (int, error) {
	f, err := os.Open(archive)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", BootArchive, err)
	}
	keep := map[string]bool{}
	for _, k := range Keep {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(k))); err == nil {
			keep[k] = true
		}
	}
	var written []string
	undo := func() {
		for _, p := range written {
			os.Remove(filepath.Join(root, filepath.FromSlash(p)) + newSuffix)
		}
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			undo()
			return 0, fmt.Errorf("%s: %w", BootArchive, err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if name == "." || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			undo()
			return 0, fmt.Errorf("%s: %q is outside the card", BootArchive, h.Name)
		}
		if keep[name] {
			continue
		}
		dst := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			undo()
			return 0, err
		}
		out, err := os.Create(dst + newSuffix)
		if err != nil {
			undo()
			return 0, err
		}
		written = append(written, name)
		_, err = io.Copy(out, tr)
		if serr := out.Sync(); err == nil {
			err = serr
		}
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			undo()
			return 0, err
		}
	}
	for i, p := range written {
		dst := filepath.Join(root, filepath.FromSlash(p))
		if err := os.Rename(dst+newSuffix, dst); err != nil {
			for _, rest := range written[i:] {
				os.Remove(filepath.Join(root, filepath.FromSlash(rest)) + newSuffix)
			}
			return i, err
		}
	}
	return len(written), nil
}

// State is what an update is doing.
type State uint8

const (
	Idle        State = iota
	Checking          // asking GitHub
	UpToDate          // nothing newer on the channel
	Downloading       // Version's boot files
	Installing        // putting them on the card
	Installed         // done: the console restarts in a moment
	Restart           // restart now
	Failed            // Err says why
)

// Status is what the dashboard shows of an update.
type Status struct {
	State       State
	Version     string // the release found
	Done, Total int64  // bytes downloaded, of how many (0 when unknown)
	Err         string
}

// Options say what an update works with.
type Options struct {
	Client  *http.Client
	API     string // API when empty
	Channel Channel
	Current string // the console's version
	Card    string // the card, written to
	Archive string // where the download goes: on the card, which has room
	// Ready is asked first: the Wi-Fi is on a network and the clock is right, or why not.
	Ready func() error
	// Sync writes out what the card holds before the restart.
	Sync func()
	// Pause is how long Installed shows before Restart.
	Pause time.Duration
}

// Updater runs one update at a time in the background.
type Updater struct {
	mu     sync.Mutex
	st     Status
	cancel context.CancelFunc
}

// Status is what the update is doing now.
func (u *Updater) Status() Status {
	if u == nil {
		return Status{}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

func (u *Updater) set(f func(*Status)) {
	u.mu.Lock()
	f(&u.st)
	u.mu.Unlock()
}

// Start looks for an update and installs it, unless one is under way.
func (u *Updater) Start(o Options) {
	u.mu.Lock()
	switch u.st.State {
	case Checking, Downloading, Installing, Installed, Restart:
		u.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	u.st = Status{State: Checking}
	u.mu.Unlock()
	go func() {
		defer cancel()
		err := u.run(ctx, o)
		if err != nil {
			if ctx.Err() != nil {
				err = errors.New("stopped")
			}
			u.set(func(s *Status) { s.State, s.Err = Failed, err.Error() })
		}
	}()
}

// Cancel stops a download. Once installing has begun it goes on to the end.
func (u *Updater) Cancel() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cancel != nil && (u.st.State == Checking || u.st.State == Downloading) {
		u.cancel()
	}
}

func (u *Updater) run(ctx context.Context, o Options) error {
	if o.API == "" {
		o.API = API
	}
	if o.Ready != nil {
		if err := o.Ready(); err != nil {
			return err
		}
	}
	r, err := Latest(ctx, o.Client, o.API, o.Channel)
	if err != nil {
		return err
	}
	if !Newer(r.Tag, o.Current) {
		u.set(func(s *Status) { s.State, s.Version = UpToDate, r.Tag })
		return nil
	}
	u.set(func(s *Status) { s.State, s.Version = Downloading, r.Tag })
	err = Fetch(ctx, o.Client, r, o.Archive, func(done, total int64) {
		u.set(func(s *Status) { s.Done, s.Total = done, total })
	})
	if err != nil {
		return err
	}
	u.set(func(s *Status) { s.State = Installing })
	_, err = Install(o.Archive, o.Card)
	os.Remove(o.Archive)
	if o.Sync != nil {
		o.Sync()
	}
	if err != nil {
		return err
	}
	u.set(func(s *Status) { s.State = Installed })
	time.Sleep(o.Pause)
	u.set(func(s *Status) { s.State = Restart })
	return nil
}
