package image

// Package is a Debian package the image is built from, pinned to one version and its
// checksum so that a build gives the same image until a pin moves.
type Package struct {
	Name    string
	Version string
	URLs    []string // the .deb; the first that answers is used
	SHA256  string
}

// Packages are the packages an image is made of: the Raspberry Pi boot firmware, a kernel
// for the boards up to the Pi 4 and the Zero 2 W (v8) and one for the Pi 5 (2712),
// Armbian's kernel for Allwinner boards and its U-Boot for the Orange Pi Zero 2W, Debian's
// arm64 kernel for QEMU's virt machine, a static busybox for the shell a card can ask for
// and for udhcpc, the Raspberry Pis' Wi-Fi firmware, and wpa_supplicant with the packages
// of the libraries it loads.
type Packages struct {
	Firmware, KernelV8, Kernel2712, KernelSunxi, UBootZero2W, KernelVirt, Busybox, WiFiFirmware Package
	WiFi                                                                                        []Package // wpa_supplicant first
}

func (p Packages) all() []Package {
	return append([]Package{p.Firmware, p.KernelV8, p.Kernel2712, p.KernelSunxi, p.UBootZero2W, p.KernelVirt, p.Busybox, p.WiFiFirmware}, p.WiFi...)
}

// Archives the pins point into. Debian's pool drops a version once a newer one replaces
// it, so its packages are also reachable on snapshot.debian.org at the date they were
// pinned; Raspberry Pi's and Armbian's pools keep old versions, and Armbian's has mirrors.
const (
	rpiPool      = "https://archive.raspberrypi.com/debian/"
	debianPool   = "https://deb.debian.org/debian/"
	snapshotPool = "https://snapshot.debian.org/archive/debian/20260916T000000Z/"
	// wifiSnapshot is the date the Wi-Fi's packages were pinned.
	wifiSnapshot = "https://snapshot.debian.org/archive/debian/20260919T000000Z/"
)

// debian is a Debian package's two places: the pool, then the snapshot of the day it was
// pinned.
func debian(snapshot, path string) []string {
	return []string{debianPool + path, snapshot + path}
}

// wifiPackage pins one of the Wi-Fi's packages from Debian 13.
func wifiPackage(name, version, path, sum string) Package {
	return Package{Name: name, Version: version, URLs: debian(wifiSnapshot, path), SHA256: sum}
}

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
	WiFiFirmware: Package{
		Name: "firmware-brcm80211", Version: "1:20260519-1~bpo13+1+rpt1",
		URLs:   []string{rpiPool + "pool/main/f/firmware-nonfree/firmware-brcm80211_20260519-1~bpo13+1+rpt1_all.deb"},
		SHA256: "c25e13e84be8dbf58b6b3381b4a10ad7e9dbeae101579376f457e0f17e60d902",
	},
	// wpa_supplicant and the packages of every library it loads, as of 2026-09-19.
	WiFi: []Package{
		wifiPackage("wpasupplicant", "2:2.10-24", "pool/main/w/wpa/wpasupplicant_2.10-24_arm64.deb", "d9ab216060c4c66cddcd53030f1a38c57e93a25cb9e0abefa6fe8f36b686f9ae"),
		wifiPackage("libc6", "2.41-12+deb13u4", "pool/main/g/glibc/libc6_2.41-12+deb13u4_arm64.deb", "8784eda966b189c777a384dac5ce009e8fc9b52d006926c5a013e7fa8aa688cc"),
		wifiPackage("libssl3t64", "3.5.7-1~deb13u2", "pool/main/o/openssl/libssl3t64_3.5.7-1~deb13u2_arm64.deb", "ec131326aa9fa9ec934eca386bc7991f328fe383eaefd3e43bd8901a9199c5ae"),
		wifiPackage("zlib1g", "1:1.3.dfsg+really1.3.1-1+b1", "pool/main/z/zlib/zlib1g_1.3.dfsg+really1.3.1-1+b1_arm64.deb", "209aa5cf671e97b9eb0410844fa6df4cae2e75b0c72e7802ab6c8ece13e6ddef"),
		wifiPackage("libzstd1", "1.5.7+dfsg-1", "pool/main/libz/libzstd/libzstd1_1.5.7+dfsg-1_arm64.deb", "924540bd59fdbfa77a0604360efdaca54411a43daf11c7e002a3c64791b67448"),
		wifiPackage("libpcsclite1", "2.3.3-1", "pool/main/p/pcsc-lite/libpcsclite1_2.3.3-1_arm64.deb", "e406cfd8f918cbe868c14a9d0dfbc0967f98861e679bcb0f30df6a49d80936f1"),
		wifiPackage("libnl-3-200", "3.7.0-2", "pool/main/libn/libnl3/libnl-3-200_3.7.0-2_arm64.deb", "0489548a052d64b1acf7f61b0f08fd2da954b6fd6b9c729a4e740d7d39652d00"),
		wifiPackage("libnl-genl-3-200", "3.7.0-2", "pool/main/libn/libnl3/libnl-genl-3-200_3.7.0-2_arm64.deb", "5455099e4ae9a013a44bce245678b50c84b93f9983cd3005f49e6a94d14e30ee"),
		wifiPackage("libnl-route-3-200", "3.7.0-2", "pool/main/libn/libnl3/libnl-route-3-200_3.7.0-2_arm64.deb", "86cd5b58c83cd70a834625f112a6c30da249bb842c4ac4daa44fd4c08298b7be"),
		wifiPackage("libdbus-1-3", "1.16.2-2", "pool/main/d/dbus/libdbus-1-3_1.16.2-2_arm64.deb", "ca6bb5f2047ad58d274cb7d14216035a07d8e5aae6f6a0122c4f6293c9c39515"),
		wifiPackage("libsystemd0", "257.13-1~deb13u1", "pool/main/s/systemd/libsystemd0_257.13-1~deb13u1_arm64.deb", "d2f63c408549c29eaefb6f4c948393edce99f45ee8e43c991391bef63fb7d54b"),
		wifiPackage("libcap2", "1:2.75-10+deb13u1+b3", "pool/main/libc/libcap2/libcap2_2.75-10+deb13u1+b3_arm64.deb", "863aa5b9cdf6f80010b48982d4639126b09a89eeef2c0c5c7be226297c811581"),
	},
}
