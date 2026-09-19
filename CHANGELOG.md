# Changelog

## Unreleased

## v0.5.0-rc.10 — 2026-09-19

Built against engine v2.0.0-rc.15. `go test ./...` and `VEDUTAOS_E2E=1 go test ./test/e2e`
pass. The first release a console can update from by itself.

### Added

- Settings: SOFTWARE CHANNEL (STABLE: releases; BETA: pre-releases too, the default on a
  pre-release), kept in `vedutaos/channel`, and INSTALL UPDATES: package `update` asks
  GitHub's API for the newest release of the channel that carries boot files, and when it
  is newer than the console's downloads `vedutaos-boot.tar.gz` onto the card, checks it
  against `image-checksums.txt`, writes every file beside the one it replaces and then
  renames them over (a failure before that leaves the card as it was), keeps `config.txt`,
  `cmdline.txt` and the buttons' overlay, syncs and restarts. The clock is set with
  busybox's ntpd when it is before 2026; HTTPS uses Mozilla's roots built into vshell.
- `vedutaos image` writes `vedutaos-boot.tar.gz`, the card's files without the games; the
  image workflow attaches it to the release. Releases before this one carry none, so a
  console updates from this version on.

## v0.5.0-rc.9 — 2026-09-19

Built against engine v2.0.0-rc.15. `go test ./...` and `VEDUTAOS_E2E=1 go test ./test/e2e`
pass, the e2e test joining a WPA2 network on QEMU's simulated radios.

### Added

- Settings, the last row of the dashboard's list, with the Wi-Fi: the networks in range
  (signal, lock, saved, joined), a password typed on an on-screen keyboard (every printable
  ASCII character, three pages), the console's state and address. Joined networks are
  remembered in `vedutaos/wifi.json` on the card, as their WPA key rather than the
  password, and joined again by themselves; a password that no longer works is forgotten.
  Package `wifi` drives wpa_supplicant through its control socket; busybox's udhcpc gets
  the address.
- The image carries Debian's wpa_supplicant with every library it loads, found from
  `DT_NEEDED` (twelve pinned packages), the Raspberry Pis' radio firmware (Raspberry Pi's
  `firmware-brcm80211`, board files as links, the 43455's alternative resolved as its
  package would), `brcmfmac` and its vendor modules, and a udhcpc script. QEMU's kernel gets
  `mac80211_hwsim`: the e2e test makes the second radio a WPA2 access point from the debug
  shell and joins it from the dashboard with the keyboard.
- The serial port says each change of screen (`vedutaos: screen wi-fi`) and what the
  Wi-Fi does (`vedutaos: wifi: …`).

### Changed

- A USB pad's Select leaves a game and its Start is the game's menu (engine rc.15); SNES
  pads such as the Rii GP100 now read A, B, Select and Start where they are. The handheld's
  Select button reports `BTN_START`, the menu. The dashboard's menu is on Start.

## v0.5.0-rc.8 — 2026-09-18

Built against engine v2.0.0-rc.14: maps draw autotiles (hand-drawn island and lake tiles,
picked by neighbours). `go test ./...` and `VEDUTAOS_E2E=1 go test ./test/e2e` pass.

## v0.5.0-rc.7 — 2026-09-18

Built against engine v2.0.0-rc.11: Lua games draw tile maps (`.vmap`, with terrain
borders), animate textures and entities by clip, and cut textures from part of a PNG.
`go test ./...` and `VEDUTAOS_E2E=1 go test ./test/e2e` pass.

## v0.5.0-rc.6 — 2026-09-18

Built against engine v2.0.0-rc.8: a game's sources are named `crate.vmodel`, `main.vscene`
and so on (the engine's `veduta upgrade` renames older ones); a game deployed with an
older tool must be upgraded first. `go test ./...` and `VEDUTAOS_E2E=1 go test ./test/e2e`
pass.

## v0.5.0-rc.5 — 2026-09-17

Built against engine v2.0.0-rc.5: Lua games save, draw accented text, animate sprite
sheets and run coroutines. `go test ./...` and `VEDUTAOS_E2E=1 go test ./test/e2e` pass.

### Added

- Saves on the card: init mounts the card read-write on `/card` and binds it read-only on
  `/boot/firmware`, and each game runs with `VEDUTA_SAVE_DIR=/card/saves/<game folder>`, the
  engine writing a synced file beside the previous save. vfat's `flush` puts a save on the
  card when it is closed; switching off syncs and unmounts both. A card that cannot be
  written is mounted read-only as before, and games cannot save.
- `vedutaos qemu --writable-card`: the card is a FAT disk made from the folder, whose saves
  are copied back when QEMU stops (QEMU's folder-backed disk corrupts the volume or stops
  on writes). The e2e test's game saves as it starts, and the test finds the save in the
  card folder after the console switched off.

## v0.5.0-rc.4 — 2026-09-17

- Built against engine v2.0.0-rc.4: Lua games can give entities parents and hitboxes, spawn
  prefabs, draw images and panels in the hud, keep assets in folders and use `math.deg`
  and `math.rad`. `go test ./...` and `VEDUTAOS_E2E=1 go test ./test/e2e` pass.

## v0.5.0-rc.3 — 2026-09-17

### Fixed

- The buttons on the Raspberry Pis. `gpio_keys` is a module on the Pi kernels and was not
  in their initramfs, so no button reached the console. And the stock `gpio-key` overlay,
  once per button, made nine input devices of one button each, most of which the player
  does not read as a pad; the image now writes `overlays/vedutaos-buttons.dtbo`, one
  gpio-keys device with every connected button, as on the Orange Pi, and `config.txt`
  loads it. Checked with `fdtoverlay` on the Pi 5's, Pi 4's and Zero 2 W's trees of the
  pinned kernel.

## v0.5.0-rc.2 — 2026-09-16

- Built against engine v2.0.0-rc.3: a Lua game on the console can build models at runtime
  (`mesh`, `volume`). `go test ./...` and `VEDUTAOS_E2E=1 go test ./test/e2e` pass on an
  image built locally.

## v0.5.0-rc.1 — 2026-09-16

Built against engine v2.0.0-rc.2: eight buttons and games written in Lua, which the console
runs itself. No game ships any more.

### Added

- Lua games: a folder whose `veduta.json` names a `script` is a game with no program. The
  dashboard lists it and runs it with the engine it is built with, as `vshell play
  <folder>` in a process of its own; a game asking for a later Lua API level than the
  console's is refused with "UPDATE VEDUTAOS". `vedutaos card`, `image` and `qemu --game`
  put a Lua project on the card as it is (scripts, manifest, card, icon, assets without the
  cooked files), with no Go, and list it as `Lua`.
- The dashboard's menu: Select opens it, A chooses, B, Cancel or Select close it. Its one
  entry, POWER OFF, switches the console off.
- The Orange Pi Zero 2W, the reference board, boots the same image: Armbian's U-Boot
  (2026.07) at 8 KiB before the partition, `extlinux/extlinux.conf`, Armbian's kernel
  6.18.44 (`sunxi/Image`) with the console's initramfs (panel-mipi-dbi, gpio_backlight,
  gpio_keys, the OTG port's musb), and the board's device tree edited at build: SPI1 with
  the panel on chip select 0, the backlight, the buttons as gpio-keys, HDMI off. Two pins
  join `image/packages.go` from Armbian's pool, with two mirrors.
- `fdt`: reads, edits and writes device tree blobs in Go; the Zero 2W's tree read back
  through it matches the original under `dtc`.
- Nine buttons on the header, on every board: Up GPIO 5, Down 6, Left 13, Right 19, A 26,
  B 21, Select 20, Cancel 16, Home 12 (Raspberry Pi numbers; the Zero 2W's header is the
  same), reporting the codes the engine reads from the handheld. On the Pis they are
  `dtoverlay=gpio-key` lines in `config.txt`'s block. `vedutaos image --pins` takes them
  (`a=17`, `home=none`) and refuses a line used twice, one of SPI or of the serial port.
- `docs/quickstart-orangepi.md`: the boot chain, the wiring of the panel, the buttons and
  the serial port, and what the board still has to prove.

### Changed

- The dashboard reads the console's buttons: the D-pad moves, A starts the game. Home does
  nothing on the console's dashboard (it means the dashboard itself); off the console the
  player's close still leaves it. The footer says `A: PLAY   SELECT: MENU`.
- The image and the release archives carry no game: the engine's demo and TinyCube are gone,
  with `.github/demos.env`.
- The end-to-end test makes an empty Lua game with the engine's `veduta init`, plays it on
  QEMU (its screen is a golden now), checks Home leaves the dashboard alone, and switches
  off from the menu.

### Verified

- `go test ./...`; an image built locally (`vedutaos image`, initrd 4.4 MB);
  `VEDUTAOS_E2E=1 go test ./test/e2e` passes (36 s): the Lua game runs until Home.
- The image built with the real Armbian packages: `eGON.BT0` at 8 KiB + 4, `sunxi/` and
  `extlinux/` on the volume, the Orange Pi initramfs (3.4 MB) with the eight modules its
  kernel does not build in; the edited tree decompiles with `dtc`.

### Not verified

- Nothing has run on an Orange Pi Zero 2W or a Raspberry Pi: U-Boot, the kernel, the panel
  on SPI1 and the buttons are unproved. No emulator runs the H618.

## v0.4.0 — 2026-09-16

TinyCube joins the engine's demo: a creative block world (five blocks, walking and flying,
breaking and placing, day and night) made with Veduta v1.4.1. The image now carries both
games, so a card written with it plays at once, and every release archive has them in
`games`.

### Added

- `vedutaos image --game` (repeatable) puts games onto the image's card: game folders,
  release archives or Veduta projects, as `vedutaos card` takes them.
- The image and release workflows build the engine's demo and TinyCube (the tag pinned in
  `.github/demos.env`, v0.1.0) for the console and ship them: in the image's `games` and in
  each archive's `games/demo` and `games/tinycube`.

### Changed

- Built against engine v1.4.1 (from v1.1.1): the dashboard draws as before (its goldens
  are unchanged), and the engine's demo is v1.4.1's template, a streamed world with hills,
  a pond, grass and flowers.

### Verified

- `go test ./...`; an image built locally with both games (`vedutaos image --game …`), whose
  card lists `games/demo` and `games/tinycube`; `VEDUTAOS_E2E=1 go test ./test/e2e` passes
  (46 s). TinyCube booted on QEMU from the dashboard: it started, walked, broke a block,
  flew and came back to the dashboard on Home.

### Not verified

- On a Raspberry Pi, as for v0.3.0: the panel, the pad, and whether TinyCube holds 20 Hz on
  a Pi Zero 2 W (under emulation it ran slower than real time, as every game does).

## v0.3.0 — 2026-09-15

VedutaOS is bare Linux. The image is one FAT32 volume with the Raspberry Pi firmware, the
Pi kernels and, for each, an initramfs holding the console: the dashboard, which is now the
system's init, busybox, the panel's firmware and the drivers. No Raspberry Pi OS, no root
file system, no systemd, udev, ssh, apt or users; the card is read-only, so the power can
be cut at any time. The image is built from five pinned Debian packages, without root,
chroot or emulation, in seconds.

### Added

- `vshell` as PID 1 (`cmd/vshell/init_linux.go`): mounts `/proc`, `/sys` and `/dev`, loads
  the modules the image lists in order, finds the card by reading the label of every block
  device (a `VEDUTA` volume first, the boot volume `VEDUTAOS` after two seconds), mounts it
  read-only, applies `vedutaos/env`, takes the framebuffer from the text console, reaps
  orphans, and switches off when the dashboard is left (`reboot`/`poweroff` by signal too).
  Every step is a `vedutaos: …` line on the kernel log, which is the serial port.
- `vedutaos/debug` on the card (`--debug`): a busybox shell on the serial port and on tty2,
  respawned. The shells also open by themselves when the dashboard fails three times.
- `image/packages.go` pins the packages (raspi-firmware, linux-image-rpi-v8 and -2712
  6.18.50, Debian's linux-image-arm64 6.12.107, busybox-static) by URL and sha256; Debian's
  are also reachable on snapshot.debian.org. Modules are chosen by each kernel's own
  `.modinfo` (the packages carry no `modules.dep`) and stored uncompressed, since the Pi
  kernels cannot decompress them.
- The dashboard rescans the card every two seconds while shown, so a card mounted late
  appears, and the card's `vedutaos/release` says which image wrote it.

### Changed

- `vedutaos image` needs xz and mtools, not root, and runs on macOS too; `--size` sets the
  volume's size (2048 MiB). The chroot script, the systemd units and the overlay are gone.
- `vedutaos qemu` boots `vmlinuz` with `initrd.img` and the card folder, nothing else: no
  image, no qcow2 overlay, no network, no ssh; `--image`, `--fresh`, `--ssh-key` and
  `--ssh-port` are gone, `--kernel` and `--initrd` name the pair, `--debug` asks for shells.
  Without `--kernel` it fetches the release's pair (about 40 MB), so Windows needs neither
  xz nor 7-Zip for it.
- `vedutaos card`: `--ssh-key` is gone with ssh; the card is the drive named `VEDUTAOS`.
- `test/e2e` reads the console's state from the serial log instead of ssh, and ends by
  switching the console off and seeing QEMU exit.
- The Pi's `cmdline.txt` names `tty1` then `serial0`, so `/dev/console` (where the
  dashboard and the games write) is the serial port; `panic=10` reboots after a panic.

### Not yet verified

- On a Raspberry Pi: that the Pi firmware boots the FAT and that the initramfs's modules
  are the right ones for the panel (`spi-bcm2835`, or the Pi 5's RP1 SPI driver, and
  `panel-mipi-dbi`), that the built-in USB and MMC drivers find the pad and the card.

## v0.2.0 — 2026-09-15

VedutaOS is an image. One `vedutaos.img.xz`, Raspberry Pi OS Lite with the console
installed, for a card and for QEMU alike; the first release whose console has been booted
and played end to end, in CI, on every change.

### Added

- `vedutaos image` builds the image on Linux; the `image` workflow builds it on a runner,
  boots it, plays, and attaches it to a tagged release with the kernel and initramfs QEMU
  boots and a checksum file.
- `vedutaos flash <device>` writes the image to a card on Linux and macOS, fetching this
  release's once; Windows uses Raspberry Pi Imager.
- `test/e2e`: the image booted headless on QEMU, the dashboard's screen against a
  reference, a game started on A and ended on Home.
- A USB stick labelled `VEDUTA` is taken as the card in place of the boot partition, and
  `vedutaos/authorized_keys` on the card admits ssh as `veduta`.
- `vedutaos qemu` gives the machine a USB tablet beside the keyboard. A game built with
  engine v1.1.0 or later reads the pointer as the console's analog stick (the middle of the
  window is rest), so the stick can be tried in emulation; the tablet follows the host
  pointer without grabbing it. `docs/quickstart-qemu.md` lists the keyboard and mouse
  stand-ins for the console's controls.

### Changed

- `vedutaos qemu` without `--image` fetches this release's image; there is no card
  `--target`, no user-data, no Debian cloud image and no UEFI firmware to find.
- The console is an image, `vedutaos.img`, built by `vedutaos image` from Raspberry Pi OS
  Lite (64-bit): the dashboard, its units, the panel's firmware and overlay, the user
  `veduta` admitted by ssh key, no first-boot dialog, and Debian's arm64 kernel so that the
  same image boots on QEMU. cloud-init provisioning of a stock card is gone, and with it
  the two ways a console was made.
- `vedutaos qemu` boots the image (built in `out/`, or `--image`) with a card of games; no
  Debian cloud image is downloaded and no UEFI firmware is needed.
- `vedutaos card <dir>` writes games, and only when asked settings (`--scale`, `--fb`,
  `--pad`), ssh keys (`--ssh-key`) and a dashboard replacing the image's (`--vshell`).
  A card is the boot partition, a USB stick labelled `VEDUTA`, or a folder for QEMU.
- Built against engine v1.1.1. The dashboard closes on Home as on Select+Start; the demo a
  release ships is the engine's v1.1.0 template, which walks with the stick.

## v0.1.0 — 2026-09-14

The first release: a console card made from a PC, for QEMU or a Raspberry Pi, with nothing
typed on the machine.

### Added

- `vedutaos qemu [--game …]` makes a card and boots it on QEMU's arm64 `virt` machine. It
  downloads Debian 13's generic arm64 image once, checked against Debian's SHA512SUMS,
  creates the machine's disk as an overlay, and shows the card folder as a FAT disk labelled
  `CIDATA`. cloud-init installs the console on the first boot.
- `vedutaos card <dir> [--game …]` writes the console onto the boot partition of a
  Raspberry Pi OS Lite (64-bit) card:
  - the dashboard and its settings;
  - the ILI9341 panel's start-up firmware and `mipi-dbi-spi` overlay (pins, rotation,
    colour order, inversion and SPI speed are flags, `--panel none` for HDMI);
  - cloud-init's first-boot files and `userconf.txt`;
  - two kernel settings.

  It refuses a folder that is not a boot partition, or a `user-data` another tool wrote.
- `--game` takes a game folder, a release archive (`.tar.gz` or `.zip`) or a Veduta project,
  which is then built for linux/arm64. The games are listed as the dashboard will list them,
  and games that are not arm64 programs are pointed out.
- `vedutaos version`.
- Release archives for Windows (amd64) and Linux (amd64, arm64). Each holds `vedutaos`,
  the dashboard for the console (`vshell-arm64`), the engine's demo game built for the
  console (`games/demo`), and the guides.

### Changed

- The dashboard is built against engine v1.0.0. The engine commit it followed before never
  read a key or a button, so the dashboard could not be driven.

### Verified

- On a four-core x86-64 Linux server under TCG: `vedutaos qemu --game <engine template>`.
  The first boot reached the dashboard in about 190 s. A later boot took about two minutes
  and ran no second setup. Dashboard, Enter, the demo (a gem collected), Ctrl+Q and back to
  the dashboard were driven through QEMU's monitor, and the screendumps were checked.

### Not yet verified

- On a Raspberry Pi: that the overlay and firmware bring the panel up the right way round
  and with the right colours, that no first-boot wizard appears, and that a Zero 2 W holds
  32 MHz SPI.
- On a Windows PC: QEMU found and started from its installer's folder, and the card folder
  shown through vvfat. The command line is covered by tests; it has not been run on
  Windows.
