package image

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// The image is one MBR partition holding a FAT32 volume. It starts 1 MiB in, where every
// partitioning tool puts a first partition, and is typed as FAT32 with LBA addressing,
// which the Raspberry Pi boot ROM and every PC read.
const (
	sectorSize    = 512
	partitionLBA  = 2048
	partitionType = 0x0c
)

// mbr returns the image's first sector: the partition table with the one partition, from
// partitionLBA to the end of the disk.
func mbr(totalSectors, id uint32) []byte {
	b := make([]byte, sectorSize)
	binary.LittleEndian.PutUint32(b[0x1b8:], id)
	e := b[0x1be : 0x1be+16]
	e[0] = 0x00                         // not marked bootable: the boot ROM does not care
	e[1], e[2], e[3] = 0xfe, 0xff, 0xff // CHS start, beyond what CHS can address
	e[4] = partitionType
	e[5], e[6], e[7] = 0xfe, 0xff, 0xff
	binary.LittleEndian.PutUint32(e[8:], partitionLBA)
	binary.LittleEndian.PutUint32(e[12:], totalSectors-partitionLBA)
	b[510], b[511] = 0x55, 0xaa
	return b
}

// mtools runs an mtools program on the image's partition.
func mtools(img string, prog string, args ...string) error {
	all := append([]string{"-i", img + "@@" + strconv.Itoa(partitionLBA*sectorSize)}, args...)
	cmd := exec.Command(prog, all...)
	cmd.Env = append(os.Environ(), "MTOOLS_SKIP_CHECK=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", prog, strings.Join(all, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// formatFAT makes the partition a FAT32 volume with 4 KiB clusters and the label given.
func formatFAT(img string, totalSectors uint32, label string) error {
	return mtools(img, "mformat", "-F", "-c", "8", "-h", "64", "-s", "32", "-H", strconv.Itoa(partitionLBA),
		"-T", strconv.FormatUint(uint64(totalSectors-partitionLBA), 10), "-v", label, "::")
}

// copyToFAT copies everything in dir onto the volume, folders and all.
func copyToFAT(img, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	args := []string{"-s", "-m", "-Q"}
	for _, e := range entries {
		args = append(args, filepath.Join(dir, e.Name()))
	}
	if len(entries) == 0 {
		return nil
	}
	return mtools(img, "mcopy", append(args, "::/")...)
}

// listFAT returns the paths on the volume, one per line, as mdir prints them.
func listFAT(img string) ([]string, error) {
	cmd := exec.Command("mdir", "-i", img+"@@"+strconv.Itoa(partitionLBA*sectorSize), "-/", "-b", "::/")
	cmd.Env = append(os.Environ(), "MTOOLS_SKIP_CHECK=1")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("mdir: %w", err)
	}
	var paths []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			paths = append(paths, strings.TrimPrefix(l, "::"))
		}
	}
	return paths, nil
}

// CardDiskSize is the size of a card disk for QEMU: enough FAT32 clusters, and sparse.
const CardDiskSize = 512 << 20

// CardDisk writes img, a disk holding one FAT32 volume labelled label with everything in
// dir on it: a card QEMU can write to, as its folder-backed disk cannot reliably be.
func CardDisk(img, dir, label string) error {
	if err := Check(); err != nil {
		return err
	}
	f, err := os.Create(img)
	if err != nil {
		return err
	}
	total := uint32(CardDiskSize / sectorSize)
	_, err = f.Write(mbr(total, 0x56454455))
	if err == nil {
		err = f.Truncate(CardDiskSize)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := formatFAT(img, total, label); err != nil {
		return err
	}
	return copyToFAT(img, dir)
}

// CopyFromCardDisk copies the folder rel of a card disk, with everything in it, into dir,
// replacing what is there. A disk without the folder copies nothing.
func CopyFromCardDisk(img, rel, dir string) error {
	paths, err := listFAT(img)
	if err != nil {
		return err
	}
	found := false
	for _, p := range paths { // folders are listed with a trailing slash
		if strings.EqualFold(strings.Trim(p, "/"), rel) {
			found = true
		}
	}
	if !found {
		return nil
	}
	return mtools(img, "mcopy", "-s", "-n", "-o", "-Q", "::/"+rel, dir+string(filepath.Separator))
}
