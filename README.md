# VedutaOS

A small console that plays [Veduta](https://github.com/riftbane/veduta) games: a Raspberry
Pi behind a 320×240 panel, a gamepad, and a dashboard listing the games present on its SD
card. Games arrive by dragging a folder onto the card from a PC — there is no store, no
installer and no network.

VedutaOS is an image, `vedutaos.img.xz`: Raspberry Pi OS Lite (64-bit) with the console
installed. Written to a card it is a Raspberry Pi console; booted on QEMU it is the same
console on an emulated machine, which is how it is tested. Each release carries the image
and `vedutaos`, the program that puts games on cards and boots the image on a PC.

**Not yet run on a Raspberry Pi.** The image boots and plays end to end on an emulated ARM64
machine, in this repository's CI, at every change. The panel, the pad and the boards
themselves are still to be proved on hardware.

## Making a console

Download the [latest release](https://github.com/riftbane/vedutaos/releases/latest).

```sh
# a Raspberry Pi: write the image, then put a game on the card's boot partition
sudo vedutaos flash /dev/sdX            # Windows: Raspberry Pi Imager, "Use custom"
vedutaos card /media/me/bootfs --game games/demo

# an emulated machine on this PC: fetches the image once, then boots it
vedutaos qemu --game games/demo
```

The steps, the panel's wiring and what to check when something is wrong are in
[VedutaOS on a Raspberry Pi](docs/quickstart-pi.md) and [VedutaOS on QEMU](docs/quickstart-qemu.md).

## The hardware it is for

- Raspberry Pi 5 now, Raspberry Pi Zero 2 W next: both are 64-bit, so one image serves them.
- A 320×240 SPI panel with an ILI9341 controller, refreshed 20 times a second.
- A wired USB gamepad (a Rii GP100), read as ordinary Linux input.

## The card

The card is the image's boot partition, which a PC sees as a small FAT drive, or any USB
stick labelled `VEDUTA`, which the console takes instead when it is plugged in. On it:

| Path | What it is |
|---|---|
| `games/<name>/` | one folder per game: its program, its assets, a `card.json` |
| `vedutaos/env` | settings overriding the console's (`VEDUTA_SCALE`, `VEDUTA_FB`, `VEDUTA_PAD`), optional |
| `vedutaos/authorized_keys` | public keys admitted as the user `veduta` over ssh, optional |
| `vedutaos/vshell` | a dashboard replacing the image's while it is there: how a console is updated from a PC |

`vedutaos card <dir>` writes these; a folder dragged into `games` works just as well.

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

## Building and testing

Everything here is standard-library Go, built without cgo, and the unit tests need no
hardware:

```sh
go test ./...
```

The image is built on Linux, as root, from the pinned Raspberry Pi OS Lite release
(`image/build.go`), with `qemu-user-static` registered so the image's own programs run in
the chroot; a GitHub Actions runner has everything:

```sh
sudo go run ./cmd/vedutaos image      # out/vedutaos.img, and vmlinuz + initrd.img for QEMU
go run ./cmd/vedutaos qemu --game …   # boots out/vedutaos.img
VEDUTAOS_E2E=1 go test ./test/e2e     # boots it headless and plays: the test of the image
```

The `image` workflow does all three on every change to what goes into the image, and
attaches the image to a tagged release. For what the emulator proves and what it cannot
(the SPI panel, and the Pi 5 itself, are not emulated by anything), see
[testing without hardware](docs/testing-without-hardware.md).

## Licence

MIT.
