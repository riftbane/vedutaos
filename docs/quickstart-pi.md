# VedutaOS on a Raspberry Pi

Write the image to a card, put a game on it, switch the Pi on. Nothing is typed on the Pi.

## 1. Write the image

Get `vedutaos.img.xz` from the [latest release](https://github.com/riftbane/vedutaos/releases/latest),
and `vedutaos` for your PC from the same page (`vedutaos_<version>_windows_amd64.zip`,
`…_linux_amd64.tar.gz`; on Windows unpack it to a folder such as `C:\vedutaos` and run it
from a Command Prompt there).

- **Windows:** [Raspberry Pi Imager](https://www.raspberrypi.com/software/), **Use custom**,
  pick `vedutaos.img.xz`, then the card. Skip the customisation when Imager offers it.
- **Linux, macOS:** `sudo vedutaos flash /dev/sdX` (the whole card, as `lsblk` or
  `diskutil list` shows it; `/dev/rdiskN` on a Mac). Without a downloaded image it fetches
  this release's once. Imager works here too.

The image is 2 GB: one FAT32 volume. A larger card's remaining space is simply not used.

## 2. Put a game on the card

Take the card out and put it back in. The PC shows it as a drive named `VEDUTAOS`
(`E:` on Windows). Then:

```bat
vedutaos card E:\ --game games\demo
```

`--game` takes a game folder, a release archive (`gems_v1.2.0_linux_arm64.tar.gz`) or a
Veduta project (built for the console, which needs Go), and can be repeated. Dragging a
game's folder into `games` does the same. `vedutaos` lists the games as the dashboard will.

Other things a card can carry, each written only when asked:

- `--debug`: a shell on the serial port (GPIO 14 and 15, 115200 baud, through a USB-serial
  adapter) and on tty2, for looking at the board.
- `--scale`, `--fb`, `--pad`: the dashboard's settings, in `vedutaos/env`.
- `--vshell vshell-arm64`: a newer dashboard than the image's, run from the card.

## 3. Switch it on

The dashboard appears on the panel within seconds. Arrows or the D-pad move, A starts a
game, Home (or Select and Start together) returns to the dashboard; on the dashboard it
switches the console off. The card is never written, so the power can be cut at any time.

A USB stick labelled `VEDUTA` with a `games` folder on it is a card too: plugged in at
start, the console lists its games instead of the SD card's.

## Wiring the panel

The image expects an ILI9341 module on SPI0 wired like this. Pin numbers are the 40-pin
header's; GPIO numbers are BCM's.

| Panel | Raspberry Pi |
|---|---|
| VCC | 3.3 V (pin 1) |
| GND | ground (pin 6) |
| SCK / CLK | GPIO 11, SPI0 SCLK (pin 23) |
| SDI / MOSI / SDA | GPIO 10, SPI0 MOSI (pin 19) |
| CS | GPIO 8, SPI0 CE0 (pin 24) |
| DC / RS | GPIO 24 (pin 18) |
| RESET / RST | GPIO 25 (pin 22) |
| LED / BL | GPIO 18 (pin 12) |
| SDO / MISO | not needed |

Wired differently, or with a panel that needs other settings, edit the marked block at the
end of `config.txt` on the card (`dtparam=reset-gpio=…,dc-gpio=…,backlight-gpio=…`,
`speed=…`), or build an image with them: `vedutaos image --pins dc=22,reset=27,backlight=none
--rotate 270 --rgb --invert --spi-speed 16000000`. Without a panel there is no picture: the
image loads no HDMI driver.

## When something is wrong

Put `--debug` on the card and connect the serial port. Everything the console does is one
line of the kernel log, `vedutaos: …`: the modules loaded and the ones that failed, the
card it found, the framebuffers, the dashboard, each game. In the shell:

```sh
dmesg | grep vedutaos                     # the console's own lines
dmesg | grep -i -e mipi -e firmware -e spi   # the panel driver and its start-up file
cat /sys/class/graphics/fb*/name          # the panel is the one named panel-mipi-dbi
cat /etc/vedutaos-release                 # which image this is
```

- *The panel stays white and the log says `no framebuffer`:* the panel's driver did not
  start. Check the wiring, then `dmesg` for `vedutaos-ili9341.bin` and `spi`.
- *`module … : … failed`:* a driver the console counted on is not in this kernel as a
  module; say which, it is a build fix.
- *The panel shows a garbled picture:* lower `speed=` in `config.txt`'s block to 16000000,
  then check CS and DC.
- *Kernel messages on the panel:* the text console was not taken away in time; the
  dashboard takes it away again every two seconds.

## What still needs checking on real hardware

None of this has been run on a Pi yet. The console boots and plays on QEMU with Debian's
kernel; the Pi kernels, the firmware and the device trees are as Raspberry Pi OS ships
them, unpacked from its packages. On a board, check:

- the boot firmware loads the kernel and the initramfs from the FAT and init runs;
- the modules the image carries are the right ones (`panel-mipi-dbi`, `spi-bcm2835` on
  the boards up to the Pi 4, the SPI driver of the Pi 5's RP1), and nothing else is missing;
- the dashboard appears on the panel, the right way up and with the right colours;
- a USB pad is read;
- a Zero 2 W's panel holds 32 MHz on SPI;
- a USB stick labelled `VEDUTA` is taken as the card.
