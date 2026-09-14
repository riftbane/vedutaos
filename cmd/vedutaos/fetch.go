package main

import (
	"bufio"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// The emulated console runs Debian's generic arm64 cloud image. Unlike the "nocloud" image
// it carries cloud-init, and its kernel has the virtio GPU the console draws on.
const (
	imageBase = "https://cloud.debian.org/images/cloud/trixie/latest/"
	imageName = "debian-13-generic-arm64.qcow2"
)

// fetchImage downloads name from base into dst, checked against the SHA512SUMS published
// beside it. Nothing is left at dst unless the whole file arrived and matched.
func fetchImage(base, name, dst string, out io.Writer) error {
	want, err := publishedSum(base+"SHA512SUMS", name)
	if err != nil {
		return err
	}
	resp, err := http.Get(base + name)
	if err != nil {
		return fmt.Errorf("download %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", base+name, resp.Status)
	}
	part := dst + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	h := sha512.New()
	p := &progress{out: out, total: resp.ContentLength, name: name}
	_, err = io.Copy(io.MultiWriter(f, h, p), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(part)
		return fmt.Errorf("download %s: %w", name, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(part)
		return fmt.Errorf("download %s: its SHA-512 is %s, but %sSHA512SUMS says %s", name, got, base, want)
	}
	return os.Rename(part, dst)
}

// publishedSum reads the checksum of name from a SHA512SUMS file.
func publishedSum(url, name string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		sum, file, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		if ok && strings.TrimLeft(file, " *") == name && len(sum) == 128 {
			return strings.ToLower(sum), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s does not list %s", url, name)
}

// progress reports a download every tenth of the way.
type progress struct {
	out   io.Writer
	name  string
	total int64
	done  int64
	shown int64
}

func (p *progress) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.total > 0 {
		if tenth := p.done * 10 / p.total; tenth > p.shown {
			p.shown = tenth
			fmt.Fprintf(p.out, "  %s: %d%% of %d MB\n", p.name, tenth*10, p.total>>20)
		}
	}
	return len(b), nil
}
