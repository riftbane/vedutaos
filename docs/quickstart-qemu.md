# VedutaOS on QEMU

Boot the console on an emulated ARM64 machine, on a Windows, Linux or macOS PC. You get the
image a Raspberry Pi gets: the dashboard, the card, launching a game and coming back, driven
from the keyboard. You do not get the SPI panel, which nothing emulates (see
[testing without hardware](testing-without-hardware.md)).

## 1. Install QEMU

- **Windows:** the installer from <https://qemu.weilnetz.de/w64/>. `vedutaos` finds it in
  `C:\Program Files\qemu` without touching `PATH`. Also [7-Zip](https://7-zip.org), to
  unpack the image.
- **Debian, Ubuntu:** `sudo apt install qemu-system-arm qemu-utils xz-utils`.
- **Fedora:** `sudo dnf install qemu-system-aarch64 qemu-img xz`.
- **macOS:** `brew install qemu xz`.

## 2. Get vedutaos

Download the archive for your PC from the
[latest release](https://github.com/riftbane/vedutaos/releases/latest):
`vedutaos_<version>_windows_amd64.zip`, `…_linux_amd64.tar.gz` or `…_linux_arm64.tar.gz`.
It holds `vedutaos`, `vshell-arm64` (the dashboard, for updating a card), `games\demo`
(the engine's demo, built for the console) and these guides.

On Windows, unpack it into a folder of your choosing, for example `C:\vedutaos`. If Windows
refuses to run a program from it, right-click the zip, choose **Properties**, tick
**Unblock**, and unpack it again. Then open a Command Prompt in that folder:

```bat
cd /d C:\vedutaos
vedutaos version
```

In PowerShell a program in the current folder is started as `.\vedutaos`.

## 3. Boot the console with the demo

```bat
vedutaos qemu --game games\demo
```

`--game` also takes another game folder, a release archive, or a Veduta project (built for
the console, if Go is installed), and can be repeated. From the `vedutaos` source folder,
`go run ./cmd/vedutaos qemu …` boots `out/vedutaos.img` instead, the image
`vedutaos image` built there.

The first time, `vedutaos`:

1. fetches this release's `vedutaos.img.xz`, `vmlinuz` and `initrd.img` (about 700 MB in
   all), checks them against the release's checksums, and unpacks the image with `xz`
   into your cache directory. On Windows, unpack `vedutaos.img.xz` with 7-Zip and pass
   `--image C:\path\vedutaos.img`, with `vmlinuz` and `initrd.img` beside it;
2. makes the card, a folder in your cache directory, holding the games, `VEDUTA_SCALE=4`
   (a quarter of the 1280×960 screen each way is the panel's 320×240) and your ssh key
   when `--ssh-key` names one;
3. creates the machine's disk as an overlay on the image, so the image stays as fetched;
4. starts QEMU with the image's Debian kernel, a 1280×960 screen, a USB keyboard and
   tablet, and the card folder shown to the machine as a disk labelled `VEDUTA`.

The machine boots to the dashboard. Under emulation on a four-core x86-64 PC that takes
about four minutes, most of it the kernel and udev; nothing needs you. On an ARM64 Linux PC
with KVM, or a Mac with Apple silicon, it takes seconds.

## Using it

- In the QEMU window: arrows or W/S move, Enter or Space start a game, **Ctrl+Q** leaves
  a game.
- In a game, the keyboard and the mouse stand in for the console's controls: W A S D or
  the arrows are the D-pad; the mouse pointer is the analog stick (the middle of the window
  is rest, its edges are the ends); Space, Escape, F and R are A, B, X and Y; Tab and Enter
  are Select and Start; Ctrl+Q is Home.
- In the terminal: the machine's serial console. **Ctrl-A x** stops the machine. With a key
  given by `--ssh-key`, `ssh -p 2222 veduta@127.0.0.1` logs in (the port is open on this
  PC only; `sudo` needs no password).
- A screenshot without touching the window: **Ctrl-A c** opens QEMU's monitor, then
  `screendump shot.ppm`, then **Ctrl-A c** to go back.

## Changing something

Run `vedutaos qemu` again with the same flags. It writes the games and settings onto the
card again and boots the machine. Games already on the card stay there. The card folder is
read when QEMU starts, so a change made while the machine runs shows at the next start.

- `--fresh` throws the machine's disk away: the next boot is the image's first.
- `--image FILE` boots another image; `--card DIR` keeps the card in a folder of yours.
- `--vshell FILE` puts a dashboard on the card that replaces the image's.
- `--display sdl` or `--display gtk` picks QEMU's window; `--display none` has no window.
- `--print` makes the card and prints the QEMU command without running it.
- Anything after `--` is passed to QEMU:
  `vedutaos qemu -- -monitor tcp:127.0.0.1:4444,server,nowait`.

## When something is wrong

Over ssh, or on the serial console once a key is on the card:

```sh
systemctl status vedutaos vedutaos-card   # the dashboard, and how the card was found
sudo journalctl -u vedutaos               # what the dashboard said
cat /sys/class/graphics/fb0/name          # expect virtio_gpudrmfb
for d in /sys/class/input/event*/device; do echo "$d $(cat $d/name)"; done
```

- *The dashboard is there but no games:* the card holds no folder with a program in it.
  `vedutaos` lists the games as the dashboard will each time it runs.
- *A login prompt, no dashboard:* `journalctl -u vedutaos`; then try `--fresh`.
- *The boot stops in an emergency shell:* the machine was too slow for its own timeouts,
  which happens on a PC busy with something else. Stop it and boot again.
- *QEMU says the card is too big:* QEMU presents the card as FAT16, which holds about 500 MB.
  Keep fewer games on the card you use in the emulator.
