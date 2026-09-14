// Package install puts games onto a card, in whichever form a person has them:
//
//   - a game folder, as a release archive unpacks or as it already sits on another card,
//     copied as it is;
//   - a release archive, <name>_<tag>_linux_arm64.tar.gz (or a .zip of a game folder),
//     unpacked;
//   - a Veduta project, built for the console the way its release workflow builds it.
//
// A game replaces the folder of the same name and nothing else; the card's other games are
// left alone.
package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"debug/elf"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/riftbane/vedutaos/card"
)

// Builder compiles a project's game for the console: the Go package entry, relative to
// dir, into the file out.
type Builder func(dir, entry, out string) error

// GoBuild is the Builder that runs the Go toolchain, with the flags a game's release
// workflow uses.
func GoBuild(dir, entry, out string) error {
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", out, entry)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build %s: %w", entry, err)
	}
	return nil
}

// Game puts the game at src into gamesDir and returns the folder it now has there.
func Game(gamesDir, src string, build Builder) (string, error) {
	fi, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("install: %w", err)
	}
	// A builder runs in the project's folder, so the destination must not be relative.
	if gamesDir, err = filepath.Abs(gamesDir); err != nil {
		return "", fmt.Errorf("install: %w", err)
	}
	if err := os.MkdirAll(gamesDir, 0o755); err != nil {
		return "", fmt.Errorf("install: %w", err)
	}
	var (
		name  string
		stage func(dst string) error
	)
	lower := strings.ToLower(src)
	switch {
	case fi.IsDir():
		if m, ok := project(src); ok {
			name = m.Name
			stage = func(dst string) error { return buildProject(src, m, dst, build) }
		} else {
			abs, err := filepath.Abs(src)
			if err != nil {
				return "", fmt.Errorf("install: %w", err)
			}
			name = filepath.Base(abs)
			stage = func(dst string) error { return copyTree(src, dst) }
		}
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		name, stage, err = fromTar(src)
	case strings.HasSuffix(lower, ".zip"):
		name, stage, err = fromZip(src)
	default:
		return "", fmt.Errorf("install: %s is not a game folder, a Veduta project, a .tar.gz or a .zip", src)
	}
	if err != nil {
		return "", fmt.Errorf("install %s: %w", src, err)
	}
	if !safeName(name) {
		return "", fmt.Errorf("install %s: %q cannot be a folder on the card", src, name)
	}
	// Build the new folder beside the old one and swap it in only when it is complete, so a
	// failure leaves the card as it was.
	dst := filepath.Join(gamesDir, name)
	partial := filepath.Join(gamesDir, "."+name+".partial")
	if err := os.RemoveAll(partial); err != nil {
		return "", fmt.Errorf("install: %w", err)
	}
	if err := stage(partial); err != nil {
		os.RemoveAll(partial)
		return "", fmt.Errorf("install %s: %w", src, err)
	}
	if err := os.RemoveAll(dst); err != nil {
		return "", fmt.Errorf("install: %w", err)
	}
	if err := os.Rename(partial, dst); err != nil {
		return "", fmt.Errorf("install: %w", err)
	}
	return dst, nil
}

// safeName reports whether a name is one plain folder name.
func safeName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.HasPrefix(s, ".") && !strings.ContainsAny(s, `/\:*?"<>|`)
}

// manifest is the part of the engine's veduta.json a project is built from. It is read
// leniently: the engine's manifest grows fields that this package need not know.
type manifest struct {
	Name   string `json:"name"`
	Entry  string `json:"entry"`
	Icon   string `json:"icon"`
	Assets string `json:"assets"`
	Cooked string `json:"cooked"`
}

// project reports whether dir is a Veduta project: a veduta.json whose entry is a package
// inside the folder. A game folder carries veduta.json too, but no source.
func project(dir string) (manifest, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "veduta.json"))
	if err != nil {
		return manifest{}, false
	}
	var m manifest
	if json.Unmarshal(data, &m) != nil || m.Entry == "" || !filepath.IsLocal(filepath.FromSlash(m.Entry)) {
		return manifest{}, false
	}
	fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(m.Entry)))
	if err != nil || !fi.IsDir() {
		return manifest{}, false
	}
	if m.Assets == "" {
		m.Assets = "assets"
	}
	return m, true
}

// buildProject stages a project as its release workflow does: the program named after the
// game, veduta.json, README.md, card.json, icon.png, and the assets without cooked files.
func buildProject(dir string, m manifest, dst string, build Builder) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entry := m.Entry
	if !strings.HasPrefix(entry, "./") && !strings.HasPrefix(entry, "../") {
		entry = "./" + entry
	}
	if err := build(dir, entry, filepath.Join(dst, m.Name)); err != nil {
		return err
	}
	for _, f := range []string{"veduta.json", "README.md"} {
		if err := copyFile(filepath.Join(dir, f), filepath.Join(dst, f), 0o644); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	desc, err := cardFor(dir, m)
	if err != nil {
		return err
	}
	if m.Icon != "" {
		if err := copyFile(filepath.Join(dir, filepath.FromSlash(m.Icon)), filepath.Join(dst, "icon.png"), 0o644); err != nil {
			return fmt.Errorf("veduta.json names the icon %s: %w", m.Icon, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dst, card.File), desc, 0o644); err != nil {
		return err
	}
	assets := filepath.Join(dir, filepath.FromSlash(m.Assets))
	if _, err := os.Stat(assets); err != nil {
		return nil // a game may embed everything
	}
	cooked := ""
	if m.Cooked != "" {
		cooked = filepath.Clean(filepath.Join(dir, filepath.FromSlash(m.Cooked)))
	}
	return copyTreeExcept(assets, filepath.Join(dst, "assets"), cooked)
}

// cardFor returns the card.json a built project carries: the project's own when it has one,
// else one made from the manifest, naming the program and the icon the folder holds.
func cardFor(dir string, m manifest) ([]byte, error) {
	desc := map[string]any{}
	data, err := os.ReadFile(filepath.Join(dir, card.File))
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &desc); err != nil {
			return nil, fmt.Errorf("%s: %w", card.File, err)
		}
		if desc["veduta"] != card.Format {
			return nil, fmt.Errorf("%s: not %s", card.File, card.Format)
		}
	case errors.Is(err, fs.ErrNotExist):
		desc = map[string]any{"veduta": card.Format, "title": m.Name, "name": m.Name}
	default:
		return nil, err
	}
	if _, ok := desc["exec"]; !ok {
		desc["exec"] = m.Name
	}
	if m.Icon != "" {
		desc["icon"] = "icon.png"
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(desc); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// fromTar opens a release archive: one folder at the top, holding the game.
func fromTar(src string) (string, func(string) error, error) {
	top, err := archiveTop(func(visit func(string) error) error {
		return walkTar(src, func(h *tar.Header, _ io.Reader) error { return visit(h.Name) })
	})
	if err != nil {
		return "", nil, err
	}
	return top, func(dst string) error {
		return walkTar(src, func(h *tar.Header, r io.Reader) error {
			rel, err := inside(top, h.Name)
			if err != nil || rel == "" {
				return err
			}
			switch h.Typeflag {
			case tar.TypeDir:
				return os.MkdirAll(filepath.Join(dst, rel), 0o755)
			case tar.TypeReg:
				return writeFile(filepath.Join(dst, rel), r, fs.FileMode(h.Mode))
			default:
				return fmt.Errorf("%s: links and devices do not belong on a card", h.Name)
			}
		})
	}, nil
}

func walkTar(src string, fn func(*tar.Header, io.Reader) error) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if err := fn(h, tr); err != nil {
			return err
		}
	}
}

// fromZip opens a zip of a game folder, laid out as a release archive is.
func fromZip(src string) (string, func(string) error, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return "", nil, err
	}
	defer zr.Close()
	top, err := archiveTop(func(visit func(string) error) error {
		for _, f := range zr.File {
			if err := visit(f.Name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	return top, func(dst string) error {
		zr, err := zip.OpenReader(src)
		if err != nil {
			return err
		}
		defer zr.Close()
		for _, f := range zr.File {
			rel, err := inside(top, f.Name)
			if err != nil {
				return err
			}
			if rel == "" {
				continue
			}
			if f.FileInfo().IsDir() {
				if err := os.MkdirAll(filepath.Join(dst, rel), 0o755); err != nil {
					return err
				}
				continue
			}
			if !f.Mode().IsRegular() {
				return fmt.Errorf("%s: links and devices do not belong on a card", f.Name)
			}
			r, err := f.Open()
			if err != nil {
				return err
			}
			err = writeFile(filepath.Join(dst, rel), r, f.Mode())
			r.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}, nil
}

// archiveTop returns the one folder every entry of an archive lies in, refusing entries
// that would land anywhere else.
func archiveTop(entries func(visit func(string) error) error) (string, error) {
	top := ""
	err := entries(func(name string) error {
		clean, err := cleanEntry(name)
		if err != nil {
			return err
		}
		first, _, _ := strings.Cut(clean, "/")
		if top == "" {
			top = first
		} else if first != top {
			return fmt.Errorf("the archive holds %s and %s at its top; a game archive holds one folder", top, first)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if top == "" {
		return "", errors.New("the archive is empty")
	}
	return top, nil
}

// cleanEntry returns an archive entry's path, refusing one that climbs out of the archive.
func cleanEntry(name string) (string, error) {
	clean := path.Clean(strings.ReplaceAll(name, `\`, "/"))
	if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, ":") {
		return "", fmt.Errorf("%s: outside the game's folder", name)
	}
	return clean, nil
}

// inside returns an entry's path below the archive's top folder, as a local path.
func inside(top, name string) (string, error) {
	clean, err := cleanEntry(name)
	if err != nil {
		return "", err
	}
	if clean == top {
		return "", nil
	}
	rel, ok := strings.CutPrefix(clean, top+"/")
	if !ok {
		return "", fmt.Errorf("%s: outside %s", name, top)
	}
	return filepath.FromSlash(rel), nil
}

func writeFile(dst string, r io.Reader, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	perm := fs.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func copyFile(src, dst string, mode fs.FileMode) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	return writeFile(dst, f, mode)
}

func copyTree(src, dst string) error {
	return copyTreeExcept(src, dst, "")
}

// copyTreeExcept copies a folder, leaving out the folder skip (a cleaned path) when it is
// inside. Links are refused: a card is FAT, which has none.
func copyTreeExcept(src, dst, skip string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skip != "" && filepath.Clean(p) == skip {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return copyFile(p, target, info.Mode())
		default:
			return fmt.Errorf("%s: links and devices do not belong on a card", p)
		}
	})
}

// Arch names the architecture a program was built for, as Go spells it, or says why it is
// not a Linux program at all.
func Arch(p string) (string, error) {
	f, err := elf.Open(p)
	if err != nil {
		return "", fmt.Errorf("%s is not a Linux program", filepath.Base(p))
	}
	defer f.Close()
	switch f.Machine {
	case elf.EM_AARCH64:
		return "arm64", nil
	case elf.EM_X86_64:
		return "amd64", nil
	case elf.EM_ARM:
		return "arm", nil
	case elf.EM_386:
		return "386", nil
	default:
		return strings.ToLower(strings.TrimPrefix(f.Machine.String(), "EM_")), nil
	}
}
