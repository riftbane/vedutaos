package image

// Package is a Debian package the image is built from, pinned to one version and its
// checksum so that a build gives the same image until a pin moves.
type Package struct {
	Name    string
	Version string
	URLs    []string // the .deb; the first that answers is used
	SHA256  string
}

// Packages are the seven packages an image is made of: the Raspberry Pi boot firmware, a
// kernel for the boards up to the Pi 4 and the Zero 2 W (v8) and one for the Pi 5 (2712),
// Armbian's kernel for Allwinner boards and its U-Boot for the Orange Pi Zero 2W, Debian's
// arm64 kernel for QEMU's virt machine, and a static busybox for the shell a card can ask
// for.
type Packages struct {
	Firmware, KernelV8, Kernel2712, KernelSunxi, UBootZero2W, KernelVirt, Busybox Package
}

func (p Packages) all() []Package {
	return []Package{p.Firmware, p.KernelV8, p.Kernel2712, p.KernelSunxi, p.UBootZero2W, p.KernelVirt, p.Busybox}
}

// Archives the pins point into. Debian's pool drops a version once a newer one replaces
// it, so its packages are also reachable on snapshot.debian.org at the date they were
// pinned; Raspberry Pi's and Armbian's pools keep old versions, and Armbian's has mirrors.
const (
	rpiPool      = "https://archive.raspberrypi.com/debian/"
	debianPool   = "https://deb.debian.org/debian/"
	snapshotPool = "https://snapshot.debian.org/archive/debian/20260916T000000Z/"
)

// armbianPools are Armbian's apt pool and two of its mirrors.
var armbianPools = []string{"https://apt.armbian.com/", "https://mirrors.dotsrc.org/armbian-apt/", "https://mirrors.tuna.tsinghua.edu.cn/armbian/"}

func armbian(path string) []string {
	urls := make([]string, len(armbianPools))
	for i, pool := range armbianPools {
		urls[i] = pool + path
	}
	return urls
}

// Stock is the pinned set, from Raspberry Pi OS trixie, Armbian trixie and Debian 13 as of
// 2026-09-16.
var Stock = Packages{
	Firmware: Package{
		Name: "raspi-firmware", Version: "1:1.20260907-1",
		URLs:   []string{rpiPool + "pool/main/r/raspi-firmware/raspi-firmware_1.20260907-1_all.deb"},
		SHA256: "2b12a0fe6f4c1e620e6677e30175e62a8ebbeeffc38001c9f3b67cfd8c5691f0",
	},
	KernelV8: Package{
		Name: "linux-image-6.18.50+rpt-rpi-v8", Version: "1:6.18.50-1+rpt1",
		URLs:   []string{rpiPool + "pool/main/l/linux/linux-image-6.18.50+rpt-rpi-v8_6.18.50-1+rpt1_arm64.deb"},
		SHA256: "8d44827fea22875e0f801252bdbc0f541c03144e970caafa3115bf485ff3c4bd",
	},
	Kernel2712: Package{
		Name: "linux-image-6.18.50+rpt-rpi-2712", Version: "1:6.18.50-1+rpt1",
		URLs:   []string{rpiPool + "pool/main/l/linux/linux-image-6.18.50+rpt-rpi-2712_6.18.50-1+rpt1_arm64.deb"},
		SHA256: "41a73543781af93f591adddcbf1e2fcc478450784ba2a27be2f07af84ce2bbb6",
	},
	KernelSunxi: Package{
		Name: "linux-image-current-sunxi64", Version: "26.8.3 (6.18.44)",
		URLs:   armbian("pool/main/l/linux-6.18.44/linux-image-current-sunxi64_26.8.3_arm64__6.18.44-S1efe-D5397-P5708-C4e0c-H2153-HK01ba-Vc222-B4990-R448a.deb"),
		SHA256: "d0c9253a77d1fbe94790cd1259ef0982c85aa24cfac0b334b8e63f9df5426b25",
	},
	UBootZero2W: Package{
		Name: "linux-u-boot-orangepizero2w-current", Version: "26.8.3 (U-Boot 2026.07)",
		URLs:   armbian("pool/main/l/linux-u-boot-orangepizero2w-current/linux-u-boot-orangepizero2w-current_26.8.3_arm64__2026.07-Sece3-P7ee6-H8076-Va1c3-B5da4-R448a.deb"),
		SHA256: "87e1590b8d6be7cebd000d11c8cffbd386d0331afea002048386bd6ca2055efa",
	},
	KernelVirt: Package{
		Name: "linux-image-6.12.107+deb13-arm64", Version: "6.12.107-1",
		URLs: []string{
			debianPool + "pool/main/l/linux-signed-arm64/linux-image-6.12.107+deb13-arm64_6.12.107-1_arm64.deb",
			snapshotPool + "pool/main/l/linux-signed-arm64/linux-image-6.12.107+deb13-arm64_6.12.107-1_arm64.deb",
		},
		SHA256: "20c92f3f97dd466e0400a6fcb792bde85af842fbb5e8b471370065e3de9d9fc9",
	},
	Busybox: Package{
		Name: "busybox-static", Version: "1:1.37.0-6+b9",
		URLs: []string{
			debianPool + "pool/main/b/busybox/busybox-static_1.37.0-6+b9_arm64.deb",
			snapshotPool + "pool/main/b/busybox/busybox-static_1.37.0-6+b9_arm64.deb",
		},
		SHA256: "c833be48abfa16bc19c4966ec93e289ff1ce5d2f1476cad3a57bd105378cd15c",
	},
}
