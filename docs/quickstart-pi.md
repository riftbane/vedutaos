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

## 2. Put a game on the card

Take the card out and put it back in. The PC shows its boot partition as a small drive
(`bootfs`, `E:` on Windows). Then:

```bat
vedutaos card E:\ --game games\demo
```

`--game` takes a game folder, a release archive (`gems_v1.2.0_linux_arm64.tar.gz`) or a
Veduta project (built for the console, which needs Go), and can be repeated. Dragging a
game's folder into `games` does the same. `vedutaos` lists the games as the dashboard will.

Other things a card can carry, each written only when asked:

- `--ssh-key C:\Users\me\.ssh\id_ed25519.pub`: log in as `veduta` over ssh (a Pi 5 on
  Ethernet; the console itself needs no network).
- `--scale`, `--fb`, `--pad`: the dashboard's settings, in `vedutaos/env`.
- `--vshell vshell-arm64`: a newer dashboard than the image's, run from the card.

## 3. Switch it on

The dashboard appears on the panel. Arrows or the D-pad move, A starts a game, Home (or
Select and Start together) returns to the dashboard. The first start takes a little longer:
the card's root partition grows to fill the card.

A USB stick labelled `VEDUTA` with a `games` folder on it is a card too: plugged in at
start, the console lists its games instead of the boot partition's.

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
end of `config.txt` on the boot partition (`dtparam=reset-gpio=…,dc-gpio=…,backlight-gpio=…`,
`speed=…`), or build an image with them: `vedutaos image --pins dc=22,reset=27,backlight=none
--rotate 270 --rgb --invert --spi-speed 16000000`. Without a panel, the dashboard is on HDMI
when the panel's framebuffer is absent; if a phantom panel takes its place, `--fb /dev/fb0`
on the card names the HDMI one.

## When something is wrong

Over ssh (a key given with `--ssh-key`, user `veduta`, `sudo` without a password):

```sh
systemctl status vedutaos vedutaos-card   # the dashboard, and how the card was found
sudo journalctl -u vedutaos               # what the dashboard said
cat /sys/class/graphics/fb*/name          # the panel is the one that is not vc4drmfb
dmesg | grep -i -e mipi -e firmware       # the panel driver and its start-up file
cat /etc/vedutaos-release                 # which image this is
```

- *The Pi shows a login prompt instead of the dashboard:* `journalctl -u vedutaos`.
- *The dashboard is on HDMI and the panel stays white:* the panel's driver did not start.
  Check the wiring, then `dmesg` for `vedutaos-ili9341.bin`.
- *The panel shows a garbled picture:* lower `speed=` in `config.txt`'s block to 16000000,
  then check CS and DC.

## What still needs checking on real hardware

None of this has been run on a Pi yet. The image boots and plays on QEMU with the Debian
kernel it also carries; the Pi kernels, the panel overlay and the firmware are as Raspberry
Pi OS ships them, untouched by the build. On a board, check:

- the first start reaches the dashboard, with no user wizard and no login prompt;
- the dashboard appears on the panel, the right way up and with the right colours;
- no text cursor blinks over it;
- a Zero 2 W's panel holds 32 MHz on SPI;
- a USB stick labelled `VEDUTA` is taken as the card.
