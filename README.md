# VedutaOS

A small console that plays [Veduta](https://github.com/riftbane/veduta) games: an Orange Pi
Zero 2W (or a Raspberry Pi) behind a 320×240 panel, nine buttons or a gamepad, and a
dashboard listing the games present on its SD card. Games arrive by dragging a folder onto the card from a PC — there is no store, no
installer and no network.

VedutaOS is an image, `vedutaos.img.xz`, and it is bare Linux: one FAT32 volume holding
the Raspberry Pi firmware and kernels, Armbian's kernel for the Orange Pi with its U-Boot
before the volume, and for each kernel an initramfs with the console in it — the dashboard, which is also the system's init, a static busybox, the
panel's firmware and a dozen drivers. No distribution, no root file system, no service
manager, no users, no network. The card is read-only to everything but the games' saves,
which are written to a synced file beside the previous save, so the console survives having
its power cut. The same console, in an initramfs for Debian's arm64 kernel, boots on
QEMU, which is how it is tested. Each release carries the image, that kernel and initramfs,
and `vedutaos`, the program that puts games on cards and boots the console on a PC.

**Not yet run on a board.** The console boots and plays end to end on an emulated ARM64
machine, in this repository's CI, at every change. The panel, the buttons, the pad and the
boards themselves are still to be proved on hardware.

## Making a console

Download the [latest release](https://github.com/riftbane/vedutaos/releases/latest).

```sh
# a Raspberry Pi: write the image, then put games on its card
sudo vedutaos flash /dev/sdX            # Windows: Raspberry Pi Imager, "Use custom"
vedutaos card /media/me/VEDUTAOS --game mygame   # a game folder, archive or Veduta project

# an emulated machine on this PC: fetches the kernel and the console once, then boots
vedutaos qemu --game mygame
```

The steps, the wiring and what to check when something is wrong are in
[VedutaOS on an Orange Pi Zero 2W](docs/quickstart-orangepi.md),
[VedutaOS on a Raspberry Pi](docs/quickstart-pi.md) and [VedutaOS on QEMU](docs/quickstart-qemu.md).

## The hardware it is for

- The Orange Pi Zero 2W (Allwinner H618, 1 GB), the reference board; the Raspberry Pi 5 and
  Zero 2 W boot the same image.
- A 320×240 SPI panel with an ILI9341 controller, refreshed 20 times a second.
- Nine buttons on the header (D-pad, A, B, Select, Cancel, Home), read through the kernel's
  gpio-keys, or a wired USB gamepad; the same wiring on every board.

## The card

The card is the image's volume, which a PC sees as a FAT drive named `VEDUTAOS`, or any
USB stick labelled `VEDUTA`, which the console takes instead when it is plugged in. On it:

| Path | What it is |
|---|---|
| `games/<name>/` | one folder per game: its program or its Lua scripts, its assets, a `card.json` |
| `saves/<name>/` | each game's saves, written by the game (`<save>.json`); copy them to keep a game's progress |
| `vedutaos/env` | settings overriding the console's (`VEDUTA_SCALE`, `VEDUTA_FB`, `VEDUTA_PAD`), optional |
| `vedutaos/debug` | while present, a shell on the serial port and on tty2, optional |
| `vedutaos/vshell` | a dashboard replacing the image's while it is there: how a console is updated from a PC |
| `vedutaos/release` | which image wrote the card |

`vedutaos card <dir>` writes these; a folder dragged into `games` works just as well. The
image's own files (`config.txt` with the panel's block, the kernels, the firmware) sit
beside them, as on any Raspberry Pi card.

A game's `card.json`:

```json
{
  "veduta": "card/1",
  "title": "Cave of Gems",
  "name": "gems",
  "version": "v1.2.0",
  "exec": "game",
  "icon": "icon.png"
}
```

`card.json` is deliberately **not** the engine's manifest. The engine's manifest is strict
and gains fields as the engine grows, so a console built today could not read one written
by a newer engine. This description never changes: `card/1` is frozen, and a console will
still list a game written years from now.

Nothing in it is required. A folder with no description, or with a damaged one, is still
listed under its folder name and still launched if something in it can run — a console
never hides a game it could play. What it cannot do is launch a folder with no program in
it, and such a folder is passed over rather than shown as broken.

`exec` and `icon` name files inside the folder and nothing else: a card comes from a card,
and a card comes from anywhere. Without `exec`, the build for the board's own architecture
(`game-arm64`) is preferred, then a plain `game`, so one card can serve two boards.

A game written in Lua has no program. Its `veduta.json` names the main script
(`"script": "main.lua"`), and the console runs it with the engine it is built with, in a
process of its own (`vshell play <folder>`); `exec` is not needed. A game whose `"api"` is
later than the console's Lua API level is listed, and says to update VedutaOS when started.

## The controls

On the dashboard the D-pad moves, A starts the game, and Select opens the menu (A chooses,
B or Cancel closes it), whose POWER OFF switches the console off. In a game, Home returns
to the dashboard; on the dashboard it does nothing. A keyboard stands in for the buttons:
arrows or W A S D, Space or Z for A, X or Shift for B, Enter or Tab for Select, Escape or
Backspace for Cancel, Ctrl+Q for Home.

## How the console starts

The Pi firmware loads `kernel8.img` (or `kernel_2712.img` on a Pi 5) and its initramfs
from the card; on the Orange Pi, U-Boot loads `sunxi/Image`, `sunxi/initrd.img` and the
board's device tree, as `extlinux/extlinux.conf` says. The kernel runs `/init`, which is `vshell`: it mounts `/proc`, `/sys` and
`/dev`, loads the modules in `/lib/modules/order`, waits for a volume labelled `VEDUTA`
or, failing that, `VEDUTAOS`, mounts it read-write on `/card` and binds it read-only on
`/boot/firmware` (a card that cannot be written is mounted read-only alone, and games cannot
save), gives each game `VEDUTA_SAVE_DIR=/card/saves/<name>`, applies the card's
settings, takes the framebuffer away from the text console, and shows the dashboard. It
reaps the processes that come back to it, opens the shells when the card asks, and
switches the machine off when the menu's POWER OFF is chosen. Every step is one line on the kernel
log, which is the serial port.

## Building and testing

Everything here is standard-library Go, built without cgo, and the unit tests need no
hardware:

```sh
go test ./...
```

The image is built from seven pinned Debian packages (`image/packages.go`: the Raspberry Pi
firmware and kernels, Armbian's sunxi64 kernel and Orange Pi Zero 2W U-Boot, Debian's arm64
kernel, busybox), downloaded and unpacked without
installing anything. It needs `xz` and mtools, no root and no emulator, and takes under a
minute once the packages are cached:

```sh
go run ./cmd/vedutaos image      # out/vedutaos.img, and vmlinuz + initrd.img for QEMU
go run ./cmd/vedutaos qemu --game …   # boots out/vmlinuz with out/initrd.img
VEDUTAOS_E2E=1 go test ./test/e2e     # boots it headless and plays: the test of the image
```

The `image` workflow does all three on every change to what goes into the image, and
attaches the image to a tagged release. For what the emulator proves and what it cannot
(the SPI panel, and the Pi 5 itself, are not emulated by anything), see
[testing without hardware](docs/testing-without-hardware.md).

## Licence

MIT.
