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
