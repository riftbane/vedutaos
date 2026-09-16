package image

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"debug/elf"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// modules is a kernel's module tree as its package ships it: the .ko files, compressed or
// not, under kernel/ and modules.builtin. The package carries no modules.dep (depmod writes
// that when the package is installed), so what a module needs is read from the module
// itself, from the .modinfo section every one carries.
type modules struct {
	dir     string            // .../usr/lib/modules/<release>
	release string            // the kernel's uname -r
	files   map[string]string // module name → path of its file, relative to dir
	builtin map[string]bool   // names built into the kernel
	info    map[string][]string
}

// moduleName is a module's name as the kernel spells it: the file name without its
// extensions, with dashes as underscores.
func moduleName(file string) string {
	base := filepath.Base(file)
	base = base[:strings.Index(base+".ko", ".ko")]
	return strings.ReplaceAll(base, "-", "_")
}

// loadModules reads the module tree of the kernel unpacked under root, which holds one
// release under usr/lib/modules (Debian, Raspberry Pi) or lib/modules (Armbian).
func loadModules(root string) (*modules, error) {
	releases, _ := filepath.Glob(filepath.Join(root, "usr", "lib", "modules", "*", "modules.builtin"))
	armbian, _ := filepath.Glob(filepath.Join(root, "lib", "modules", "*", "modules.builtin"))
	releases = append(releases, armbian...)
	if len(releases) != 1 {
		return nil, fmt.Errorf("%s: want one kernel under usr/lib/modules, found %d", root, len(releases))
	}
	m := &modules{dir: filepath.Dir(releases[0]), files: map[string]string{}, builtin: map[string]bool{}, info: map[string][]string{}}
	m.release = filepath.Base(m.dir)
	b, err := os.ReadFile(releases[0])
	if err != nil {
		return nil, err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			m.builtin[moduleName(l)] = true
		}
	}
	err = filepath.WalkDir(filepath.Join(m.dir, "kernel"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.Contains(d.Name(), ".ko") {
			return nil
		}
		rel, _ := filepath.Rel(m.dir, p)
		m.files[moduleName(p)] = filepath.ToSlash(rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return m, nil
}

// resolve returns the modules named and everything they depend on, in an order that
// loads each after what it needs. A name built into the kernel is left out; one neither
// built in nor shipped is an error, so a kernel that drops a driver is noticed at build.
func (m *modules) resolve(names []string) ([]string, error) {
	var order []string
	state := map[string]int{} // 1 visiting, 2 done
	var visit func(name string, from string) error
	visit = func(name, from string) error {
		name = strings.ReplaceAll(name, "-", "_")
		if m.builtin[name] || state[name] == 2 {
			return nil
		}
		if state[name] == 1 {
			return fmt.Errorf("modules: %s depends on itself", name)
		}
		if _, ok := m.files[name]; !ok {
			if from != "" {
				return fmt.Errorf("modules: %s needs %s, which %s neither ships nor builds in", from, name, m.release)
			}
			return fmt.Errorf("modules: %s neither ships nor builds in %s", m.release, name)
		}
		state[name] = 1
		deps, err := m.depends(name)
		if err != nil {
			return err
		}
		for _, d := range deps {
			if err := visit(d, name); err != nil {
				return err
			}
		}
		state[name] = 2
		order = append(order, name)
		return nil
	}
	for _, n := range names {
		if err := visit(n, ""); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// depends reads a module's dependencies from its .modinfo section.
func (m *modules) depends(name string) ([]string, error) {
	if deps, ok := m.info[name]; ok {
		return deps, nil
	}
	b, err := m.read(name)
	if err != nil {
		return nil, err
	}
	f, err := elf.NewFile(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("modules: %s: %w", name, err)
	}
	defer f.Close()
	sec := f.Section(".modinfo")
	if sec == nil {
		return nil, fmt.Errorf("modules: %s has no .modinfo", name)
	}
	data, err := sec.Data()
	if err != nil {
		return nil, err
	}
	var deps []string
	for _, kv := range bytes.Split(data, []byte{0}) {
		if v, ok := bytes.CutPrefix(kv, []byte("depends=")); ok {
			for _, d := range strings.Split(string(v), ",") {
				if d = strings.TrimSpace(d); d != "" {
					deps = append(deps, d)
				}
			}
		}
	}
	sort.Strings(deps)
	m.info[name] = deps
	return deps, nil
}

// read returns a module's bytes, decompressed.
func (m *modules) read(name string) ([]byte, error) {
	rel, ok := m.files[name]
	if !ok {
		return nil, fmt.Errorf("modules: no %s", name)
	}
	p := filepath.Join(m.dir, filepath.FromSlash(rel))
	switch {
	case strings.HasSuffix(p, ".ko"):
		return os.ReadFile(p)
	case strings.HasSuffix(p, ".gz"):
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		z, err := gzip.NewReader(bufio.NewReader(f))
		if err != nil {
			return nil, err
		}
		return io.ReadAll(z)
	case strings.HasSuffix(p, ".xz"), strings.HasSuffix(p, ".zst"):
		prog := "xz"
		if strings.HasSuffix(p, ".zst") {
			prog = "zstd"
		}
		out, err := exec.Command(prog, "-dc", p).Output()
		if err != nil {
			return nil, fmt.Errorf("%s -dc %s: %w", prog, p, err)
		}
		return out, nil
	}
	return nil, fmt.Errorf("modules: %s: unknown compression", rel)
}

// plainPath is where a module goes in the initramfs: its place in the tree, uncompressed
// (the Pi kernels cannot load a compressed module).
func (m *modules) plainPath(name string) string {
	rel := m.files[name]
	return "/lib/modules/" + m.release + "/" + rel[:strings.Index(rel, ".ko")+3]
}
