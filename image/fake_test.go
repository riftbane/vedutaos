package image

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// minimalELF returns the smallest ELF64 file the tests need: a type, a machine and a
// .modinfo section holding the lines given (as a kernel module carries them).
func minimalELF(typ elf.Type, machine elf.Machine, modinfo ...string) []byte {
	var info bytes.Buffer
	for _, l := range modinfo {
		info.WriteString(l)
		info.WriteByte(0)
	}
	shstr := []byte("\x00.modinfo\x00.shstrtab\x00")
	infoOff := int64(64)
	shstrOff := infoOff + int64(info.Len())
	shOff := shstrOff + int64(len(shstr))
	var b bytes.Buffer
	b.Write([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	binary.Write(&b, binary.LittleEndian, uint16(typ))
	binary.Write(&b, binary.LittleEndian, uint16(machine))
	binary.Write(&b, binary.LittleEndian, uint32(1))
	binary.Write(&b, binary.LittleEndian, uint64(0))     // entry
	binary.Write(&b, binary.LittleEndian, uint64(0))     // phoff
	binary.Write(&b, binary.LittleEndian, uint64(shOff)) // shoff
	binary.Write(&b, binary.LittleEndian, uint32(0))     // flags
	for _, v := range []uint16{64, 56, 0, 64, 3, 2} {    // ehsize, phentsize, phnum, shentsize, shnum, shstrndx
		binary.Write(&b, binary.LittleEndian, v)
	}
	b.Write(info.Bytes())
	b.Write(shstr)
	section := func(name uint32, typ elf.SectionType, off, size int64) {
		for _, v := range []any{name, uint32(typ), uint64(0), uint64(0), uint64(off), uint64(size), uint32(0), uint32(0), uint64(1), uint64(0)} {
			binary.Write(&b, binary.LittleEndian, v)
		}
	}
	section(0, elf.SHT_NULL, 0, 0)
	section(1, elf.SHT_PROGBITS, infoOff, int64(info.Len()))
	section(10, elf.SHT_STRTAB, shstrOff, int64(len(shstr)))
	return b.Bytes()
}

// module returns a fake kernel module named name that depends on deps.
func module(name string, deps ...string) []byte {
	return minimalELF(elf.ET_REL, elf.EM_AARCH64, "name="+name, "depends="+strings.Join(deps, ","), "license=GPL")
}

// fakeDeb returns a .deb holding the files given (a path ending in / is a directory, and
// content starting with "-> " a symbolic link to the rest).
func fakeDeb(files map[string][]byte) []byte {
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if strings.HasSuffix(name, "/") {
			tw.WriteHeader(&tar.Header{Name: "./" + name, Typeflag: tar.TypeDir, Mode: 0o755})
			continue
		}
		if target, ok := strings.CutPrefix(string(content), "-> "); ok {
			tw.WriteHeader(&tar.Header{Name: "./" + name, Typeflag: tar.TypeSymlink, Linkname: target, Mode: 0o777})
			continue
		}
		mode := int64(0o644)
		if strings.Contains(name, "/bin/") {
			mode = 0o755
		}
		tw.WriteHeader(&tar.Header{Name: "./" + name, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(content))})
		tw.Write(content)
	}
	tw.Close()
	gz.Close()
	var control bytes.Buffer
	cgz := gzip.NewWriter(&control)
	ctw := tar.NewWriter(cgz)
	ctw.WriteHeader(&tar.Header{Name: "./control", Typeflag: tar.TypeReg, Mode: 0o644, Size: 8})
	ctw.Write([]byte("Package\n"))
	ctw.Close()
	cgz.Close()
	var ar bytes.Buffer
	ar.WriteString("!<arch>\n")
	member := func(name string, b []byte) {
		fmt.Fprintf(&ar, "%-16s%-12d%-6d%-6d%-8s%-10d`\n", name, 0, 0, 0, "100644", len(b))
		ar.Write(b)
		if len(b)%2 == 1 {
			ar.WriteByte('\n')
		}
	}
	member("debian-binary", []byte("2.0\n"))
	member("control.tar.gz", control.Bytes())
	member("data.tar.gz/", data.Bytes()) // GNU ar ends a name with a slash; dpkg does not
	return ar.Bytes()
}

// xzCompress compresses with the xz program, as the kernel packages do their modules.
func xzCompress(t *testing.T, b []byte) []byte {
	t.Helper()
	cmd := exec.Command("xz", "-c")
	cmd.Stdin = bytes.NewReader(b)
	out, err := cmd.Output()
	if err != nil {
		t.Skip("no xz")
	}
	return out
}

// fakeKernel returns a kernel package for release rel with the modules given, each as
// name → dependencies, and the names built in.
func fakeKernel(t *testing.T, rel string, mods map[string][]string, builtin []string) map[string][]byte {
	t.Helper()
	dir := "usr/lib/modules/" + rel + "/"
	files := map[string][]byte{
		"boot/vmlinuz-" + rel:                  []byte("kernel " + rel),
		"boot/config-" + rel:                   []byte("CONFIG_X=y\n"),
		dir + "dtb/broadcom/bcm2710-x.dtb":     []byte("dtb"),
		dir + "dtb/overlays/mipi-dbi-spi.dtbo": []byte("dtbo"),
		dir + "dtb/overlays/README":            []byte("overlays"),
	}
	var b strings.Builder
	for _, n := range builtin {
		b.WriteString("kernel/x/" + n + ".ko\n")
	}
	files[dir+"modules.builtin"] = []byte(b.String())
	i := 0
	for name, deps := range mods {
		m := module(name, deps...)
		// One in three is left plain, as some packages ship them.
		if i%3 == 2 {
			files[dir+"kernel/drivers/"+name+".ko"] = m
		} else {
			files[dir+"kernel/drivers/"+strings.ReplaceAll(name, "_", "-")+".ko.xz"] = xzCompress(t, m)
		}
		i++
	}
	return files
}

// fakeSunxiKernel returns Armbian's kernel package as it is laid out: modules under
// lib/modules, device trees under usr/lib/linux-image-<release>, the real board tree.
func fakeSunxiKernel(t *testing.T, rel string, mods map[string][]string, builtin []string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	for name, b := range fakeKernel(t, rel, mods, builtin) {
		if strings.Contains(name, "/dtb/") {
			continue
		}
		files[strings.TrimPrefix(name, "usr/")] = b
	}
	dtb, err := os.ReadFile(filepath.Join("testdata", Zero2WDTB))
	if err != nil {
		t.Fatal(err)
	}
	files["usr/lib/linux-image-"+rel+"/allwinner/"+Zero2WDTB] = dtb
	return files
}
