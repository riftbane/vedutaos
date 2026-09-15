# Trying the console without a Raspberry Pi

The console boots on a PC before any hardware arrives: its init, the drivers, the card, the
dashboard, launching a game and coming back. `vedutaos qemu` does it in one command (see
[VedutaOS on QEMU](quickstart-qemu.md)), and `go test ./test/e2e` does it headless and
checks what the screen shows. What you cannot do is test the panel, and it is worth knowing
why before spending an evening on it.

## What cannot be emulated, and why

**There is no emulated Raspberry Pi 5, and there will not be one soon.** QEMU models
`raspi0` through `raspi4b`, and nothing beyond. The Pi 5 moved USB, Ethernet, GPIO, SPI and
the display interfaces into a separate chip, RP1, reached over a PCIe link. QEMU models
neither RP1 nor that PCIe controller. Its `raspi4b` machine explicitly disables the PCIe and
Ethernet blocks, so even the Pi 4 machine has no USB and no network.

**The SPI panel cannot be emulated at all.** An emulated framebuffer is 32 bits per pixel
by construction, so the RGB565 path this console uses on the real panel never runs. That
half is honest hardware work.

What emulation does give you is the other half, and it is the larger one:

- the very console a Pi runs: its init, the modules it loads by hand, the card it finds and
  mounts, its settings, on a real ARM64 kernel;
- finding a framebuffer, drawing the dashboard and reading input;
- scanning a card's game folders, launching a game, coming back, switching off.

## What `vedutaos qemu` runs

It uses QEMU's generic `virt` machine, not a Raspberry machine, and boots Debian's arm64
kernel directly with the console's initramfs, the pair `vedutaos image` writes beside the
image for this purpose; the image itself is not needed. On an x86-64 PC this is pure emulation, roughly ten times slower than native.
That does not matter for a dashboard and is noticeable for a game. On an ARM64 Linux PC
with KVM, or a Mac with Apple silicon, it uses hardware virtualisation and runs at full
speed.

The card is a folder on the PC that QEMU's `vvfat` driver shows the machine as a FAT disk
labelled `VEDUTA`; the console mounts a volume with that label as its card, exactly as it
would a USB stick. This works on a Windows host, where 9p and virtiofs cannot
be built. To see or change the exact command, run `vedutaos qemu --print`.

## Two things that will not work, whatever you try

- **A gamepad cannot be passed through on Windows.** The HID driver owns it, and detaching
  it fails. You would have to rebind the device with Zadig, which stops Windows from using
  it. Drive the console from the keyboard in emulation, and keep the pad for the board.
- **`uname -m` lying is not a kernel.** Docker Desktop with `--platform linux/arm64` runs
  ARM64 *binaries* through emulation on an x86-64 kernel. There is no `/dev/fb0` and no
  `/dev/input` to be had, so it proves nothing about this console.

## Cheaper than any of it

If you have a spare Linux laptop, boot it to a text console (Ctrl+Alt+F3). You get a real
framebuffer with a real driver and real evdev. Plug in the gamepad and you also get **the
real button codes of the pad**. They are the single most uncertain thing in this project,
and no amount of emulation can tell you them. A laptop proves everything except the ARM64
instruction set.

Build the dashboard for the laptop and run it there:

```sh
go build -o vshell ./cmd/vshell
VEDUTA_BACKEND=fbdev VEDUTAOS_GAMES=/path/to/card/games ./vshell
```

A Raspberry Pi 4 or 5 costs less than the time any substitute takes. If the target is a Pi
5, emulation is not an alternative, only a stopgap. Writing its card is described in
[VedutaOS on a Raspberry Pi](quickstart-pi.md).

## What still needs the board

- That RGB565, the byte order and the real stride are right for the ILI9341 panel.
- That writing to `/dev/fbN` reaches the glass, rather than needing mmap and an explicit
  damage call. The whole design of `Present` rests on this.
- That the panel's start-up sequence, overlay and wiring bring the panel up, the right way
  round.
- That the SPI bus sustains 20 Hz, and whether tearing shows without double buffering.
- The real button codes of the Rii GP100, including whether Select and Start are where the
  exit chord assumes.
- Unplugging the pad mid-game, and what the kernel really does on `SYN_DROPPED`.
- Legibility and colour on the real panel. The reference images pin pixel values, not what
  gamma and viewing angle do to them at 320×240.
