# VedutaOS on QEMU

Boot the console on an emulated ARM64 machine, on a Windows, Linux or macOS PC. You get the
console a Raspberry Pi runs: its init, the card, the dashboard, launching a game and coming
back, driven from the keyboard. You do not get the SPI panel, which nothing emulates (see
[testing without hardware](testing-without-hardware.md)).

## 1. Install QEMU

- **Windows:** the installer from <https://qemu.weilnetz.de/w64/>. `vedutaos` finds it in
  `C:\Program Files\qemu` without touching `PATH`.
- **Debian, Ubuntu:** `sudo apt install qemu-system-arm`.
- **Fedora:** `sudo dnf install qemu-system-aarch64`.
- **macOS:** `brew install qemu`.

## 2. Get vedutaos

Download the archive for your PC from the
[latest release](https://github.com/riftbane/vedutaos/releases/latest):
`vedutaos_<version>_windows_amd64.zip`, `…_linux_amd64.tar.gz` or `…_linux_arm64.tar.gz`.
It holds `vedutaos`, `vshell-arm64` (the console program, for updating a card) and these
guides. It holds no game: bring yours (`veduta init mygame` makes an empty one).

On Windows, unpack it into a folder of your choosing, for example `C:\vedutaos`. If Windows
refuses to run a program from it, right-click the zip, choose **Properties**, tick
**Unblock**, and unpack it again. Then open a Command Prompt in that folder:

```bat
cd /d C:\vedutaos
vedutaos version
```

In PowerShell a program in the current folder is started as `.\vedutaos`.

## 3. Boot the console with a game

```bat
vedutaos qemu --game mygame
```

`--game` takes a game folder, a release archive, or a Veduta project (a Lua game as it is,
a Go game built for the console if Go is installed), and can be repeated. From the `vedutaos` source folder,
`go run ./cmd/vedutaos qemu …` boots `out/vmlinuz` and `out/initrd.img` instead, the
kernel and console `vedutaos image` built there.

The card folder is shown to the machine read-only, so games cannot save. With
`--writable-card` the card is a FAT disk made from the folder (it needs mtools, as `vedutaos
image` does); when the machine stops, its `saves` folder is copied back into the card folder,
where the next run finds it.

The first time, `vedutaos`:

1. fetches this release's `vmlinuz` (Debian's arm64 kernel) and `initrd.img` (the console,
   about 40 MB in all) and checks them against the release's checksums. There is no disk
   image to unpack: the initramfs is the whole system;
2. makes the card, a folder in your cache directory, holding the games and `VEDUTA_SCALE=4`
   (a quarter of the 1280×960 screen each way is the panel's 320×240);
3. starts QEMU with that kernel and initramfs, a 1280×960 screen, a USB keyboard and
   tablet, and the card folder shown to the machine as a disk labelled `VEDUTA`.

The machine boots to the dashboard. Under emulation on a four-core x86-64 PC that takes a
minute or two, most of it the kernel; nothing needs you. On an ARM64 Linux PC with KVM, or
a Mac with Apple silicon, it takes seconds.

## Using it

- In the QEMU window the keyboard is the console's buttons: arrows or W A S D the D-pad,
  Space or Z A, X or Shift B, Enter or Tab Select, Escape or Backspace Cancel, **Ctrl+Q**
  Home. On the dashboard A starts a game and Select opens the menu, whose POWER OFF
  switches the console off, which ends QEMU. In a game, Ctrl+Q returns to the dashboard.
- In the terminal: the console's serial port. The lines `vedutaos: …` say what the console
  did — the modules loaded, the card found, the dashboard, each game started and ended.
  **Ctrl-A x** stops the machine at once.
- A screenshot without touching the window: **Ctrl-A c** opens QEMU's monitor, then
  `screendump shot.ppm`, then **Ctrl-A c** to go back.

## Changing something

Run `vedutaos qemu` again with the same flags. It writes the games and settings onto the
card again and boots the machine. Games already on the card stay there. The card folder is
read when QEMU starts, so a change made while the machine runs shows at the next start.

- `--kernel FILE` boots another kernel, with `initrd.img` beside it or `--initrd FILE`;
  `--card DIR` keeps the card in a folder of yours.
- `--vshell FILE` puts a console program on the card that replaces the image's dashboard.
- `--debug` asks the console for a shell on the serial port (this terminal) and on tty2.
- `--display sdl` or `--display gtk` picks QEMU's window; `--display none` has no window.
- `--print` makes the card and prints the QEMU command without running it.
- Anything after `--` is passed to QEMU:
  `vedutaos qemu -- -monitor tcp:127.0.0.1:4444,server,nowait`.

## When something is wrong

Boot with `--debug` and, in the terminal, press Enter for the shell (busybox: `ls`, `cat`,
`dmesg`, `mount` and the rest):

```sh
dmesg | grep vedutaos                  # what the console did, step by step
cat /sys/class/graphics/fb0/name       # expect virtio_gpudrmfb
for d in /sys/class/input/event*/device; do echo "$d $(cat $d/name)"; done
ls /boot/firmware/games                # the card, as the console sees it
```

- *The dashboard is there but no games:* the card holds no folder with a program in it.
  `vedutaos` lists the games as the dashboard will each time it runs.
- *`vedutaos: no card yet`:* the card's disk was not found; the `modules:` line before it
  says whether `virtio_blk` loaded.
- *A black window and no `vedutaos: dashboard` line:* read the lines before it; the
  console retries the dashboard every two seconds and opens the shells after three
  failures even without `--debug`.
- *QEMU says the card is too big:* QEMU presents the card as FAT16, which holds about 500 MB.
  Keep fewer games on the card you use in the emulator.
