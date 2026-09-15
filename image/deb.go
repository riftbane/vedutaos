package image

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// Getter fetches a URL. Build uses HTTP; the tests serve files of their own.
type Getter func(url string) (io.ReadCloser, error)

func httpGet(url string) (io.ReadCloser, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

// fetchPackage returns the package's .deb in dir, downloading it unless a copy matching
// the pin is already there. Nothing is left in dir unless it matches.
func fetchPackage(p Package, dir string, get Getter, out io.Writer) (string, error) {
	if len(p.URLs) == 0 || p.SHA256 == "" {
		return "", fmt.Errorf("package %s is not pinned", p.Name)
	}
	dst := filepath.Join(dir, path.Base(p.URLs[0]))
	if sum, err := fileSum(dst); err == nil && sum == p.SHA256 {
		fmt.Fprintf(out, "%s %s: cached\n", p.Name, p.Version)
		return dst, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var errs []string
	for _, url := range p.URLs {
		fmt.Fprintf(out, "%s %s: downloading %s\n", p.Name, p.Version, url)
		err := downloadTo(url, dst, p.SHA256, get)
		if err == nil {
			return dst, nil
		}
		errs = append(errs, err.Error())
	}
	return "", fmt.Errorf("%s: %s", p.Name, strings.Join(errs, "; "))
}

func downloadTo(url, dst, sum string, get Getter) error {
	body, err := get(url)
	if err != nil {
		return err
	}
	defer body.Close()
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), body)
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("%s: %w", url, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		os.Remove(tmp)
		return fmt.Errorf("%s: sha256 %s, want %s", url, got, sum)
	}
	return os.Rename(tmp, dst)
}

func fileSum(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// unpackDeb extracts a .deb's files into dst. A .deb is an ar archive holding
// data.tar.<xz|gz|zst>; the standard library reads ar and tar, xz and zstd are programs.
// A dst already unpacked from the same .deb (a stamp holds its checksum) is left alone.
func unpackDeb(deb, dst string) error {
	sum, err := fileSum(deb)
	if err != nil {
		return err
	}
	stamp := filepath.Join(dst, ".unpacked")
	if b, err := os.ReadFile(stamp); err == nil && string(b) == sum {
		return nil
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	f, err := os.Open(deb)
	if err != nil {
		return err
	}
	defer f.Close()
	name, r, err := arMember(f, "data.tar")
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(deb), err)
	}
	tr, done, err := decompress(name, r)
	if err != nil {
		return err
	}
	if err := untar(tr, dst); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(deb), err)
	}
	if err := done(); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(deb), err)
	}
	return os.WriteFile(stamp, []byte(sum), 0o644)
}

// arMember returns the first member of an ar archive whose name starts with prefix, as a
// reader over its bytes.
func arMember(r io.ReadSeeker, prefix string) (string, io.Reader, error) {
	var magic [8]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil || string(magic[:]) != "!<arch>\n" {
		return "", nil, errors.New("not an ar archive")
	}
	for {
		var hdr [60]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if err == io.EOF {
				return "", nil, fmt.Errorf("no %s* member", prefix)
			}
			return "", nil, err
		}
		if string(hdr[58:60]) != "`\n" {
			return "", nil, errors.New("bad ar member header")
		}
		name := strings.TrimSuffix(strings.TrimRight(string(hdr[0:16]), " "), "/")
		size, err := strconv.ParseInt(strings.TrimSpace(string(hdr[48:58])), 10, 64)
		if err != nil {
			return "", nil, fmt.Errorf("bad size of ar member %s", name)
		}
		if strings.HasPrefix(name, prefix) {
			return name, io.LimitReader(r, size), nil
		}
		if _, err := r.Seek(size+size%2, io.SeekCurrent); err != nil {
			return "", nil, err
		}
	}
}

// decompress returns a reader of the plain bytes of a compressed stream, chosen by the
// name's extension, and a function to call once it has been read to the end.
func decompress(name string, r io.Reader) (io.Reader, func() error, error) {
	none := func() error { return nil }
	switch {
	case strings.HasSuffix(name, ".gz"):
		z, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, err
		}
		return z, z.Close, nil
	case strings.HasSuffix(name, ".xz"), strings.HasSuffix(name, ".zst"):
		prog := "xz"
		if strings.HasSuffix(name, ".zst") {
			prog = "zstd"
		}
		cmd := exec.Command(prog, "-dc")
		cmd.Stdin = r
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", prog, err)
		}
		return out, func() error {
			io.Copy(io.Discard, out)
			if err := cmd.Wait(); err != nil {
				return fmt.Errorf("%s: %v: %s", prog, err, strings.TrimSpace(stderr.String()))
			}
			return nil
		}, nil
	case strings.HasSuffix(name, ".tar"):
		return r, none, nil
	}
	return nil, nil, fmt.Errorf("%s: unknown compression", name)
}

// untar extracts a tar stream into dst: directories, regular files, symbolic links and
// hard links, nothing else. Entries that would land outside dst are refused.
func untar(r io.Reader, dst string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		rel := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if rel == "." {
			continue
		}
		if path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
			return fmt.Errorf("%s: outside the package", h.Name)
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			perm := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				perm = 0o755
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			os.Remove(target)
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			src := filepath.Join(dst, filepath.FromSlash(path.Clean(strings.TrimPrefix(h.Linkname, "./"))))
			b, err := os.ReadFile(src)
			if err != nil {
				return fmt.Errorf("%s: hard link to %s: %w", h.Name, h.Linkname, err)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(target, b, os.FileMode(h.Mode).Perm()|0o600); err != nil {
				return err
			}
		}
	}
}
