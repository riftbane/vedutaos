# Trying the console without a Raspberry Pi

You can run the whole console on a Windows PC before any hardware arrives — the dashboard,
the card, launching a game and coming back. What you cannot do is test the panel, and it is
worth knowing why before spending an evening on it.

## What cannot be emulated, and why

**There is no emulated Raspberry Pi 5, and there will not be one soon.** QEMU models
`raspi0` through `raspi4b`, and nothing beyond. The Pi 5 moved USB, Ethernet, GPIO, SPI and
the display interfaces into a separate chip, RP1, reached over a PCIe link; QEMU models
neither RP1 nor that PCIe controller, and its `raspi4b` machine explicitly disables the
PCIe and Ethernet blocks, so even the Pi 4 machine has no USB and no network.

**The SPI panel cannot be emulated at all.** An emulated framebuffer is 32 bits per pixel
by construction, so the RGB565 path this console uses on the real panel never runs. That
half is honest hardware work.

What emulation does give you is the other half, and it is the larger one: a real `linux/arm64`
binary on a real ARM64 kernel, finding a framebuffer, drawing the dashboard, reading input,
scanning an SD card's game folders and launching a game.

## The route: QEMU on a generic ARM64 machine

Use `-M virt`, not a Raspberry machine. On an x86-64 PC this is pure emulation — roughly
ten times slower than native — which is irrelevant for a dashboard and noticeable for a
game. A Debian arm64 image boots to a usable system in about a minute and a half.

If your PC is itself ARM64 (a Snapdragon X machine), use Hyper-V instead and it runs at
full speed. Check with `systeminfo | findstr /C:"System Type"`.

### 1. QEMU

Install from <https://qemu.weilnetz.de/w64/> (or `pacman -S mingw-w64-ucrt-x86_64-qemu`
under MSYS2), then:

```bat
set "PATH=%PATH%;C:\Program Files\qemu"
qemu-system-aarch64.exe --version
```

### 2. A guest

The Debian *nocloud* arm64 image needs no cloud-init and logs in as root without a
password:

```bat
mkdir C:\vm && cd /d C:\vm
curl.exe -L -o debian-arm64.qcow2 https://cloud.debian.org/images/cloud/trixie/latest/debian-13-nocloud-arm64.qcow2
qemu-img.exe resize debian-arm64.qcow2 16G
```

First boot, headless, to set a password and install ssh:

```bat
qemu-system-aarch64.exe -machine virt -cpu cortex-a72 -smp 4 -m 4G ^
  -accel tcg,thread=multi -bios edk2-aarch64-code.fd ^
  -drive if=virtio,format=qcow2,file=debian-arm64.qcow2 ^
  -netdev user,id=n0,hostfwd=tcp::2222-:22 -device virtio-net-pci,netdev=n0 ^
  -display none -serial mon:stdio
```

It stops at "Press any key to proceed" and waits there for ever if you do not. Then, in the
guest: `passwd`, `apt install -y openssh-server`, `systemctl enable --now ssh`, `poweroff`.

### 3. Build on Windows, run in the guest

No Go inside the guest — cross-compile on the PC:

```bat
mkdir C:\card\games\demo
cd /d C:\src\vedutaos
set CGO_ENABLED=0&& set GOOS=linux&& set GOARCH=arm64
go build -o C:\card\vshell .\cmd\vshell
cd /d C:\src\veduta
go build -o C:\card\games\demo\game-arm64 .\template\cmd\game
(echo {"veduta":"card/1","title":"DEMO","exec":"game-arm64"}) > C:\card\games\demo\card.json
set GOOS=&& set GOARCH=
```

Boot with a framebuffer, a keyboard and the network:

```bat
qemu-system-aarch64.exe -machine virt -cpu cortex-a72 -smp 4 -m 4G ^
  -accel tcg,thread=multi -bios edk2-aarch64-code.fd ^
  -drive if=virtio,format=qcow2,file=debian-arm64.qcow2 ^
  -device virtio-gpu-pci -device qemu-xhci -device usb-kbd ^
  -netdev user,id=n0,hostfwd=tcp::2222-:22 -device virtio-net-pci,netdev=n0 ^
  -display sdl -serial mon:stdio
```

Do not add `-device ramfb` as well: two 32-bit framebuffers make the console refuse to
choose, and say so.

Copy the card in. **9p and virtiofs cannot be built for a Windows host**, so use ssh:

```bat
scp -P 2222 -r C:\card root@127.0.0.1:/root/
```

### 4. Run it

In the guest, give the framebuffer up by the text console first, or it redraws over your
frames:

```sh
chvt 2 ; echo 0 > /sys/class/vtconsole/vtcon1/bind
VEDUTA_BACKEND=fbdev VEDUTA_SCALE=4 VEDUTAOS_GAMES=/root/card/games /root/card/vshell
```

`VEDUTA_SCALE=4` renders a quarter of the pixels in each direction — close to the real
320×240 panel, and under emulation it is the difference between usable and not.

For a screenshot without looking at the window: `Ctrl-A c` for the QEMU monitor, then
`screendump C:\vm\shot.png -f png`, `Ctrl-A c` back, `Ctrl-A x` to quit.

## Two things that will not work, whatever you try

- **A gamepad cannot be passed through on Windows.** The HID driver owns it, and detaching
  it fails; you would have to rebind the device with Zadig, which stops Windows from using
  it. Drive the console from the keyboard in emulation, and keep the pad for the board.
- **`uname -m` lying is not a kernel.** Docker Desktop with `--platform linux/arm64` runs
  ARM64 *binaries* through emulation on an x86-64 kernel; there is no `/dev/fb0` and no
  `/dev/input` to be had, so it proves nothing about this console.

## Cheaper than any of it

If you have a spare Linux laptop, boot it to a text console (Ctrl+Alt+F3). You get a real
framebuffer with a real driver, real evdev, and — plug in the gamepad — **the real button
codes of the pad**, which is the single most uncertain thing in this project and which no
amount of emulation can tell you. It proves everything except the ARM64 instruction set.

And a Raspberry Pi 4 or 5 costs less than the time any substitute takes. If the target is a
Pi 5, emulation is not an alternative: it is a stopgap.

## What still needs the board

- That RGB565, the byte order and the real stride are right for the ILI9341 panel.
- That writing to `/dev/fbN` reaches the glass, rather than needing mmap and an explicit
  damage call — the whole design of `Present` rests on this.
- That the SPI bus sustains 20 Hz, and whether tearing shows without double buffering.
- The real button codes of the Rii GP100, including whether Select and Start are where the
  exit chord assumes.
- Unplugging the pad mid-game, and what the kernel really does on `SYN_DROPPED`.
- Legibility and colour on the real panel: the reference images pin pixel values, not what
  gamma and viewing angle do to them at 320×240.
