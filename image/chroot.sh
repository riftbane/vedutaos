#!/bin/sh
# Runs inside the image, as root, with the boot partition on /boot/firmware. Everything
# that needs the image's own programs happens here; the rest is done from outside.
set -eu
export DEBIAN_FRONTEND=noninteractive

# The kernel QEMU boots. The Pi kernels stay as they are: the raspi-firmware hooks only
# handle -rpi- flavours, so /boot/firmware is untouched (build.go checks that it is).
apt-get update -q
apt-get install -y -q --no-install-recommends linux-image-arm64

# One user, admitted by ssh key only. Raspberry Pi OS ships a placeholder "pi" that its
# first-boot dialog renames; the dialog is disabled, so the placeholder goes.
deluser --quiet pi 2>/dev/null || true
rm -rf /home/pi
adduser --disabled-password --gecos "" --uid 1000 veduta >/dev/null
usermod -aG sudo,video,input veduta
echo "veduta ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/010_veduta-nopasswd

# The image is the console already: no cloud-init, no first-boot dialog, no login on the
# dashboard's terminal, and host keys made on the first boot of each console.
touch /etc/cloud/cloud-init.disabled
systemctl disable userconfig.service
systemctl mask getty@tty1.service
systemctl enable vedutaos-card.service vedutaos.service ssh.service
rm -f /etc/ssh/ssh_host_*

# The initramfs of the QEMU kernel has to reach a virtio disk (the overlay's
# /etc/initramfs-tools/modules names the drivers).
update-initramfs -u -k "$(ls /boot/vmlinuz-*-arm64 | sed 's#.*/vmlinuz-##')"

apt-get clean
rm -rf /var/lib/apt/lists/*

# Free space holds what apt downloaded and what was deleted; zeroed, it compresses to
# nothing in the .img.xz.
cat /dev/zero > /zero 2>/dev/null || true
rm -f /zero
sync
