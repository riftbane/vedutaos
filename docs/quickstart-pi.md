# VedutaOS on a Raspberry Pi

Make a console card for a Raspberry Pi 5 or Zero 2 W from a Windows or Linux PC. It takes
three steps, and nothing is typed on the Pi.

## 1. Write Raspberry Pi OS Lite

In [Raspberry Pi Imager](https://www.raspberrypi.com/software/), choose your board, then
**Raspberry Pi OS (other) → Raspberry Pi OS Lite (64-bit)**, then the SD card. When Imager
offers to customise the settings, skip it. The console writes its own first-boot settings,
and `vedutaos` will not overwrite ones Imager wrote.

The image must be Trixie from November 2025 or later, when Raspberry Pi OS started reading
first-boot settings from the card with cloud-init. Imager always offers the current image.

## 2. Put the console on the card

Take the card out and put it back in. The PC shows its boot partition as a drive
(`bootfs`, say `E:`). Get `vedutaos` as described in
[VedutaOS on QEMU](quickstart-qemu.md#2-get-vedutaos). Then, in its folder, run:

```bat
vedutaos card E:\ --game games\demo
```

`--game` takes a game folder, a release archive or a Veduta project, and can be repeated.
`vedutaos` lists the games as the dashboard will list them.

It writes these files onto the partition:

| File | What it is for |
|---|---|
| `vedutaos/vshell` | the dashboard |
| `vedutaos/env` | the dashboard's settings (`VEDUTA_SCALE`, `VEDUTA_FB`, `VEDUTA_PAD`) |
| `vedutaos/vedutaos-ili9341.bin` | the panel's start-up sequence |
| `games/` | the games |
| `user-data`, `meta-data` | what the Pi does on its first start |
| `userconf.txt` | the Pi's user, so no set-up wizard waits for a keyboard |
| `config.txt` | one marked block: SPI and the panel's overlay |
| `cmdline.txt` | two settings: no text cursor, no screen blanking |

Everything else on the partition stays as Imager wrote it.

## 3. Switch it on

On its first start the Pi installs the console and restarts once. That takes a few minutes,
with the Pi's own messages on the panel or HDMI. After the restart the dashboard appears,
and it appears again at every start after that.

To add a game later, put the card in the PC and drop the game's folder into `games\`, or
run `vedutaos card E:\ --game …` again. Running `vedutaos card` again also updates the
dashboard. If you change the panel settings, the Pi sets itself up again on its next start.

## Wiring the panel

The defaults are an ILI9341 module on SPI0 wired like this. Pin numbers are the 40-pin
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

Wired differently, name the lines: `--pins dc=22,reset=27,backlight=none`. Use
`backlight=none` when the module's LED pin is tied to 3.3 V, and `reset=none` when RST is
tied high.

Other settings for the panel:

- `--rotate 270`: the picture is upside down (the default is `90`).
- `--rgb`: red and blue are swapped.
- `--invert`: colours are inverted (common on IPS modules).
- `--spi-speed 16000000`: the picture is garbled or flickers (the default is 32 MHz).
- `--panel none`: no panel yet. The dashboard uses HDMI.

## Logging in

The Pi's user is `veduta`, with no password. To reach it over ssh, pass your public key:
`--ssh-key C:\Users\me\.ssh\id_ed25519.pub`. The console needs no network itself. A Pi 5
can use Ethernet. For Wi-Fi, set it in Imager's customisation, then pass
`--replace-user-data` to `vedutaos card`. vedutaos replaces `user-data` but leaves
`network-config`, where the network settings live.

## When something is wrong

Over ssh:

```sh
cloud-init status --long                    # did the first start's setup run, and what failed
sudo journalctl -u vedutaos                 # what the dashboard said
cat /sys/class/graphics/fb*/name            # the panel is the one that is not vc4drmfb
dmesg | grep -i -e mipi -e firmware         # the panel driver and its start-up file
```

- *The Pi shows its login prompt instead of the dashboard:* the first-start setup did not
  run. Check that the card was not customised in Imager, then run `vedutaos card` again.
- *The dashboard is on HDMI and the panel stays white:* the panel's driver did not start.
  Check the wiring, then `dmesg` for `vedutaos-ili9341.bin`.
- *The panel shows a garbled picture:* try `--spi-speed 16000000`, then check CS and DC.

## What still needs checking on real hardware

None of this has been run on a Pi yet. Written from a PC, the card has been proved on QEMU,
and each file is pinned by tests. On a board, check:

- the first start finishes and restarts once, with no user wizard;
- the dashboard appears on the panel, the right way up and with the right colours;
- no text cursor blinks over it;
- with `--panel none`, the dashboard appears on HDMI;
- a Zero 2 W's panel holds 32 MHz on SPI.
