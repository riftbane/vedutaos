# Changelog

## Unreleased

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
