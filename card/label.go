package card

import (
	"encoding/binary"
	"errors"
	"io"
	"strings"
)

// ReadLabel returns the volume label of the FAT file system at r, or an error when there
// is no FAT file system there. The label a PC writes lands in the root directory (Windows
// leaves the boot sector's copy as NO NAME), so that entry is read first and the boot
// sector's field is the fallback. The console picks its card by this.
func ReadLabel(r io.ReaderAt) (string, error) {
	var bs [512]byte
	if _, err := r.ReadAt(bs[:], 0); err != nil {
		return "", err
	}
	if bs[510] != 0x55 || bs[511] != 0xaa || (bs[0] != 0xeb && bs[0] != 0xe9) {
		return "", errors.New("not a FAT file system")
	}
	bytesPerSector := int64(binary.LittleEndian.Uint16(bs[11:]))
	sectorsPerCluster := int64(bs[13])
	reserved := int64(binary.LittleEndian.Uint16(bs[14:]))
	fats := int64(bs[16])
	rootEntries := int64(binary.LittleEndian.Uint16(bs[17:]))
	fatSize := int64(binary.LittleEndian.Uint16(bs[22:]))
	fat32 := fatSize == 0
	if fat32 {
		fatSize = int64(binary.LittleEndian.Uint32(bs[36:]))
	}
	switch {
	case bytesPerSector < 512 || bytesPerSector > 4096 || bytesPerSector&(bytesPerSector-1) != 0,
		sectorsPerCluster == 0 || sectorsPerCluster&(sectorsPerCluster-1) != 0,
		reserved == 0, fats == 0 || fats > 2, fatSize == 0:
		return "", errors.New("not a FAT file system")
	}
	// The root directory: a fixed area after the FATs on FAT12/16, a cluster chain on
	// FAT32, of which the first cluster is enough for a label.
	var rootOffset, rootSize int64
	if fat32 {
		cluster := int64(binary.LittleEndian.Uint32(bs[44:]))
		if cluster < 2 {
			return "", errors.New("not a FAT file system")
		}
		rootOffset = (reserved + fats*fatSize + (cluster-2)*sectorsPerCluster) * bytesPerSector
		rootSize = sectorsPerCluster * bytesPerSector
	} else {
		rootOffset = (reserved + fats*fatSize) * bytesPerSector
		rootSize = rootEntries * 32
	}
	if rootSize > 0 && rootSize <= 1<<20 {
		dir := make([]byte, rootSize)
		if _, err := r.ReadAt(dir, rootOffset); err == nil {
			for i := 0; i+32 <= len(dir); i += 32 {
				e := dir[i : i+32]
				if e[0] == 0 {
					break
				}
				if e[0] == 0xe5 || e[11]&0x0f == 0x0f {
					continue
				}
				if e[11]&0x08 != 0 {
					return labelString(e[:11]), nil
				}
			}
		}
	}
	// The boot sector's copy, present when the extended boot signature is.
	sig, field := 38, 43
	if fat32 {
		sig, field = 66, 71
	}
	if bs[sig] == 0x29 {
		return labelString(bs[field : field+11]), nil
	}
	return "", nil
}

func labelString(b []byte) string {
	s := strings.TrimRight(string(b), " \x00")
	if s == "NO NAME" {
		return ""
	}
	return s
}
