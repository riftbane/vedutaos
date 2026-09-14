# VedutaOS on QEMU in ten minutes

The short version of [testing without hardware](testing-without-hardware.md): boot an
emulated ARM64 machine on a Windows PC and run the console in it. You get the dashboard,
the card, launching a game and coming back, driven from the keyboard. You do not get the
SPI panel, which nothing emulates.

Everything below is typed in a Windows command prompt unless it says *in the guest*.

## 1. QEMU

Install the Windows build from <https://qemu.weilnetz.de/w64/>, then:

```bat
set "PATH=%PATH%;C:\Program Files\qemu"
qemu-system-aarch64.exe --version
```

## 2. A Linux for it

```bat
mkdir C:\vm && cd /d C:\vm
curl.exe -L -o debian-arm64.qcow2 https://cloud.debian.org/images/cloud/trixie/latest/debian-13-nocloud-arm64.qcow2
qemu-img.exe resize debian-arm64.qcow2 16G
```

First boot, to set a password and get ssh. It logs in as `root` with no password:

```bat
qemu-system-aarch64.exe -machine virt -cpu cortex-a72 -smp 4 -m 4G ^
  -accel tcg,thread=multi -bios edk2-aarch64-code.fd ^
  -drive if=virtio,format=qcow2,file=debian-arm64.qcow2 ^
  -netdev user,id=n0,hostfwd=tcp::2222-:22 -device virtio-net-pci,netdev=n0 ^
  -display none -serial mon:stdio
```

It stops at **"Press any key to proceed"** and waits there for ever if you do not press
one. Then, *in the guest*:

```sh
passwd
apt update && apt install -y openssh-server
systemctl enable --now ssh
poweroff
```

## 3. Build the console and a game

No Go inside the guest — cross-compile on the PC. Both repositories side by side:

```bat
mkdir C:\card\games\demo
cd /d C:\src\vedutaos
set CGO_ENABLED=0&& set GOOS=linux&& set GOARCH=arm64
go build -o C:\card\vshell .\cmd\vshell

cd /d C:\src\veduta
go build -o C:\card\games\demo\game-arm64 .\template\cmd\game
xcopy /e /i template\assets C:\card\games\demo\assets
copy template\veduta.json C:\card\games\demo\
(echo {"veduta":"card/1","title":"DEMO","exec":"game-arm64"}) > C:\card\games\demo\card.json

set GOOS=&& set GOARCH=
```

## 4. Boot with a screen and a keyboard

```bat
cd /d C:\vm
qemu-system-aarch64.exe -machine virt -cpu cortex-a72 -smp 4 -m 4G ^
  -accel tcg,thread=multi -bios edk2-aarch64-code.fd ^
  -drive if=virtio,format=qcow2,file=debian-arm64.qcow2 ^
  -device virtio-gpu-pci -device qemu-xhci -device usb-kbd ^
  -netdev user,id=n0,hostfwd=tcp::2222-:22 -device virtio-net-pci,netdev=n0 ^
  -display sdl -serial mon:stdio
```

Do not add `-device ramfb` as well: with two 32-bit framebuffers the console refuses to
guess which is the screen, and says so.

Copy the card in (9p and virtiofs cannot be built on a Windows host, so use ssh):

```bat
scp -P 2222 -r C:\card root@127.0.0.1:/root/
```

## 5. Run it

*In the guest*, take the framebuffer away from the text console first, or it redraws over
the dashboard:

```sh
chvt 2
echo 0 > /sys/class/vtconsole/vtcon1/bind
VEDUTA_BACKEND=fbdev VEDUTA_SCALE=4 VEDUTAOS_GAMES=/root/card/games /root/card/vshell
```

- Arrows or W/S move, Space or Enter start a game, Escape leaves the dashboard.
- **Ctrl+Q closes the window** — the way out of a running game when there is no pad to
  press Select+Start on.
- `VEDUTA_SCALE=4` draws a quarter of the pixels in each direction, close to the real
  320×240 panel. Under emulation it is the difference between usable and not.

A screenshot without touching the window: `Ctrl-A c` for the QEMU monitor, then
`screendump C:\vm\shot.png -f png`, `Ctrl-A c` to go back, `Ctrl-A x` to quit.

## When something is wrong

```sh
# What the console sees as a screen: expect virtio_gpudrmfb, 1280,800, 32, 5120.
head /sys/class/graphics/fb*/name /sys/class/graphics/fb*/virtual_size \
     /sys/class/graphics/fb*/bits_per_pixel /sys/class/graphics/fb*/stride

# What it sees as input: expect "QEMU QEMU USB Keyboard".
for d in /sys/class/input/event*/device; do echo "$d $(cat $d/name)"; done
```

- *No framebuffer, or several* — name one: `VEDUTA_FB=fb0`.
- *Nothing responds* — the keyboard was not found; name it: `VEDUTA_PAD=event1`.
- *The dashboard is there but no games* — `VEDUTAOS_GAMES` points at the folder that
  *contains* the game folders, not at a game.

## Rebuilding

```bat
cd /d C:\src\vedutaos && set GOOS=linux&& set GOARCH=arm64&& go build -o C:\card\vshell .\cmd\vshell && set GOOS=&& set GOARCH=
scp -P 2222 C:\card\vshell root@127.0.0.1:/root/card/vshell
```
