package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/riftbane/vedutaos/image"
)

// A release carries the image beside the tool archives: vedutaos.img.xz for a card and
// for QEMU, with the kernel and initramfs QEMU boots and a checksum file over them.
const (
	releaseBase   = "https://github.com/riftbane/vedutaos/releases/download/"
	imageArchive  = image.ImageName + ".xz"
	checksumsName = "image-checksums.txt"
)

// Seams for tests.
var (
	releaseURL = func(version, name string) string { return releaseBase + version + "/" + name }
	httpGet    = http.Get
)

// imageDir is where a release's image is kept once fetched: <cache>/vedutaos/image/<version>.
func imageDir(version string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "vedutaos", "image", version), nil
}

// fetchRelease returns the image of a release, fetching it once: the checksum file first,
// then each file it names that is missing or does not match, and finally the unpacked
// image. A tool without a version has no release to fetch from.
func fetchRelease(version string, out io.Writer) (string, error) {
	if version == "dev" || version == "" {
		return "", errors.New("this build of vedutaos has no release to fetch an image from: pass --image, or build one with \"vedutaos image\"")
	}
	dir, err := imageDir(version)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	img := filepath.Join(dir, image.ImageName)
	sums, err := download(releaseURL(version, checksumsName), out)
	if err != nil {
		return "", fmt.Errorf("release %s: %w", version, err)
	}
	want := map[string]string{}
	for _, l := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(l); len(f) == 2 {
			want[strings.TrimPrefix(f[1], "*")] = f[0]
		}
	}
	for _, name := range []string{imageArchive, image.KernelName, image.InitrdName} {
		sum, ok := want[name]
		if !ok {
			return "", fmt.Errorf("release %s: %s does not name %s", version, checksumsName, name)
		}
		dst := filepath.Join(dir, name)
		if got, err := fileSum(dst); err == nil && got == sum {
			continue
		}
		fmt.Fprintf(out, "fetching %s of %s\n", name, version)
		if err := downloadTo(releaseURL(version, name), dst, sum, out); err != nil {
			return "", fmt.Errorf("release %s: %w", version, err)
		}
		if name == imageArchive {
			os.Remove(img)
		}
	}
	if !fileOK(img) {
		fmt.Fprintf(out, "unpacking %s\n", imageArchive)
		if err := unxz(filepath.Join(dir, imageArchive), img); err != nil {
			return "", err
		}
	}
	return img, nil
}

// unxz unpacks src to dst with the xz program: the standard library has no xz, and every
// Linux and macOS has the program; on Windows 7-Zip opens the file.
func unxz(src, dst string) error {
	xz, err := lookPath("xz")
	if err != nil {
		return fmt.Errorf("xz is not installed: unpack %s yourself (7-Zip on Windows) and pass --image %s", src, strings.TrimSuffix(src, ".xz"))
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	cmd := exec.Command(xz, "-dc", src)
	cmd.Stdout = f
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("unpacking %s: %w", src, err)
	}
	return os.Rename(tmp, dst)
}

func download(url string, out io.Writer) ([]byte, error) {
	resp, err := httpGet(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// downloadTo fetches url into dst, which is left only when whole and matching sum.
func downloadTo(url, dst, sum string, out io.Writer) error {
	resp, err := httpGet(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	p := &progress{w: out, total: resp.ContentLength}
	_, err = io.Copy(io.MultiWriter(f, h, p), resp.Body)
	f.Close()
	p.done()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		os.Remove(tmp)
		return fmt.Errorf("%s: sha256 %s, want %s", filepath.Base(dst), got, sum)
	}
	return os.Rename(tmp, dst)
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, bufio.NewReader(f)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// progress prints how far a copy is, in whole percents, on one line.
type progress struct {
	w           io.Writer
	total, n    int64
	lastPercent int
}

func (p *progress) Write(b []byte) (int, error) {
	p.n += int64(len(b))
	if p.total > 0 {
		if pc := int(p.n * 100 / p.total); pc != p.lastPercent {
			p.lastPercent = pc
			fmt.Fprintf(p.w, "\r%3d%%", pc)
		}
	} else if p.n/(64<<20) != (p.n-int64(len(b)))/(64<<20) {
		fmt.Fprintf(p.w, "\r%d MB", p.n>>20)
	}
	return len(b), nil
}

func (p *progress) done() {
	if p.n > 0 {
		fmt.Fprintln(p.w)
	}
}
