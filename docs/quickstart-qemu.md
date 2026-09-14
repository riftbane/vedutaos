# VedutaOS on QEMU

Boot the console on an emulated ARM64 machine, on a Windows or Linux PC. You get the
dashboard, the card, launching a game and coming back, driven from the keyboard. You do not
get the SPI panel, which nothing emulates (see [testing without hardware](testing-without-hardware.md)).

## 1. Install QEMU

- **Windows:** the installer from <https://qemu.weilnetz.de/w64/>. `vedutaos` finds it in
  `C:\Program Files\qemu` without touching `PATH`.
- **Debian, Ubuntu:** `sudo apt install qemu-system-arm qemu-efi-aarch64 qemu-utils`.
- **Fedora:** `sudo dnf install qemu-system-aarch64 edk2-aarch64 qemu-img`.

## 2. Get vedutaos

Download the archive for your PC from the
[latest release](https://github.com/riftbane/vedutaos/releases/latest):
`vedutaos_<version>_windows_amd64.zip` on Windows, or `…_linux_amd64.tar.gz` on Linux.
It holds:

- `vedutaos`, the program that makes cards;
- `vshell-arm64`, the dashboard for the console, which must stay beside `vedutaos`;
- `games\demo`, the engine's demo game, already built for the console;
- these guides.

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

`--game` also takes another game folder or a release archive
(`gems_v1.2.0_linux_arm64.tar.gz`), and can be repeated. It takes a Veduta project too,
which is then built for the console, but only if Go is installed. From the `vedutaos` source
folder, `go run ./cmd/vedutaos qemu …` works the same way and builds the dashboard as well.

The first time, `vedutaos`:

1. downloads Debian 13's generic arm64 image once (about 410 MB) and checks it against Debian's
   published SHA-512;
2. makes the card, a folder in your cache directory, holding the dashboard, the games and
   cloud-init's first-boot files;
3. creates the machine's disk as an overlay on the image, so the image stays untouched;
4. starts QEMU with a 1280×960 screen, a USB keyboard, and the card folder shown to the
   machine as a FAT disk labelled `CIDATA`.

On its first start the machine reads that disk, installs the console and starts the
dashboard. That takes about three minutes under emulation and needs nothing from you. Later
starts skip the setup and reach the dashboard in about two minutes. Both times were measured
on a four-core x86-64 server.

## Using it

- In the QEMU window: arrows or W/S move, Enter or Space start a game, **Ctrl+Q** leaves
  a game.
- In the terminal: the machine's serial console. Log in as `veduta`, password `veduta`, or
  press **Ctrl-A x** to stop the machine. `ssh -p 2222 veduta@127.0.0.1` works too. The
  port is only open on this PC.
- A screenshot without touching the window: **Ctrl-A c** opens QEMU's monitor, then
  `screendump shot.png -f png`, then **Ctrl-A c** to go back.

## Changing something

Run `vedutaos qemu` again with the same flags. It rebuilds what it builds, copies the new
dashboard and games onto the card, and boots the machine with them. Games already on the
card stay there. The card folder is read when QEMU starts, so a change made while the
machine runs shows at the next start.

- `--fresh` throws the machine's disk away and installs the console again from Debian's
  image.
- `--card DIR` keeps the card in a folder of your choosing.
- `--display sdl` or `--display gtk` picks QEMU's window; `--display none` has no window.
- `--print` makes the card and prints the QEMU command without running it.
- Anything after `--` is passed to QEMU:
  `vedutaos qemu -- -monitor tcp:127.0.0.1:4444,server,nowait`.

## When something is wrong

Log in on the serial console or over ssh, then:

```sh
sudo journalctl -u vedutaos        # what the dashboard said
cloud-init status --long           # whether the first-boot setup ran, and what failed
cat /sys/class/graphics/fb0/name   # expect virtio_gpudrmfb
for d in /sys/class/input/event*/device; do echo "$d $(cat $d/name)"; done
```

- *The dashboard is there but no games:* the card holds no folder with a program in it.
  `vedutaos` lists the games as the dashboard will each time it runs.
- *No dashboard, a login prompt instead:* the first-boot setup did not run. Check
  `cloud-init status --long`, then try `--fresh`.
- *QEMU says the card is too big:* QEMU presents the card as FAT16, which holds about 500 MB.
  Keep fewer games on the card you use in the emulator.
