# VedutaOS on an Orange Pi Zero 2W

The console's reference board: an Orange Pi Zero 2W (Allwinner H618, 1 GB), a 320×240
ILI9341 SPI panel and nine buttons on the 40-pin header. The same image as the Raspberry
Pis: write it, add games, switch on.

**Not yet run on the board.** What the image holds for it is checked at build (the device
tree is read back, U-Boot's place on the card, the initramfs); booting, the panel and the
buttons are still to be proved.

## 1. Write the image and add games

As for a Raspberry Pi ([VedutaOS on a Raspberry Pi](quickstart-pi.md), steps 1 and 2):
`vedutaos.img.xz` with Raspberry Pi Imager or `vedutaos flash`, then
`vedutaos card E:\ --game mygame`.

How it boots: the H618's boot ROM reads U-Boot (Armbian's build for this board) from 8 KiB
into the card, before the partition. U-Boot reads `extlinux/extlinux.conf` from the
partition and starts Armbian's kernel (`sunxi/Image`), the console's initramfs
(`sunxi/initrd.img`) and the board's device tree with the panel, its backlight and the
buttons written in (`sunxi/sun50i-h618-orangepi-zero2w.dtb`). HDMI is switched off in that
tree.

## 2. Wiring

The Zero 2W's header has the Raspberry Pi's layout, and the image names its lines by the
Raspberry Pi's GPIO numbers, so the wiring is the same on both boards. The panel:

| Panel | Header pin | GPIO | H618 |
|---|---|---|---|
| VCC | 1 | 3.3 V | |
| GND | 6 | ground | |
| SCK / CLK | 23 | 11 | PH6, SPI1 CLK |
| SDI / MOSI / SDA | 19 | 10 | PH7, SPI1 MOSI |
| CS | 24 | 8 | PH5, SPI1 CS0 |
| DC / RS | 18 | 24 | PH4 |
| RESET / RST | 22 | 25 | PI6 |
| LED / BL | 12 | 18 | PI1 |

Each button between its pin and ground (the pins are pulled up; a press reads low):

| Button | Header pin | GPIO | H618 |
|---|---|---|---|
| Up | 29 | 5 | PI0 |
| Down | 31 | 6 | PI15 |
| Left | 33 | 13 | PI12 |
| Right | 35 | 19 | PI2 |
| A | 37 | 26 | PI16 |
| B | 40 | 21 | PI3 |
| Select | 38 | 20 | PI4 |
| Cancel | 36 | 16 | PC12 |
| Home | 32 | 12 | PI11 |

The serial console (UART0, 115200 baud) is on pins 8 (TX) and 10 (RX), beside ground on
pin 6.

Wired differently: `vedutaos image --pins dc=22,a=17,home=none …` builds an image with
other lines (GPIO numbers; `none` for a line not connected), and `--rotate`, `--rgb`,
`--invert`, `--spi-speed` as for the Pi. The SPI lines and the serial port cannot be
changed.

## 3. Switch it on

The dashboard appears on the panel. The D-pad moves, A starts a game, Home returns to the
dashboard, Select (reported as Start, the menu) on the dashboard opens the menu, whose POWER
OFF switches the console off. Switch it off that way rather than cutting the power: games
save on the card. The Wi-Fi screen says there is no Wi-Fi: the board's radio has no driver
in the image yet.

## What still needs checking on the board

- U-Boot starts from the card and boots the entry of `extlinux.conf`;
- the kernel finds the card (`vedutaos: card /dev/mmcblk0p1`) and loads the modules;
- the panel comes up (`vedutaos: framebuffers: fb0 panel-mipi-dbidrmfb`), the right way up,
  at 32 MHz;
- every button reaches the dashboard, and Home leaves a game;
- a USB pad on the host port is read;
- battery life with the dashboard and with a game.
