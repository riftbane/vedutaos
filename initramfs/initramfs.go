// Package initramfs names what the console's initramfs holds. The build (package image)
// writes it and init (cmd/vshell) reads it; nothing else needs to agree on these paths.
package initramfs

const (
	Init       = "/init"                 // the dashboard, which is PID 1
	Modprobe   = "/sbin/modprobe"        // a link to init, which loads a module the kernel asks for by name
	Busybox    = "/bin/busybox"          // a shell and its tools, for a card that asks
	ModuleList = "/lib/modules/order"    // the modules init loads, one path per line, in order
	Release    = "/etc/vedutaos-release" // the image's version
	Firmware   = "/lib/firmware"         // where the kernel looks for the panel's start-up file
	CardMount  = "/boot/firmware"        // where the card is mounted, read-only
	CardWrite  = "/card"                 // the same card mounted read-write, where only saves are written
	SavesEnv   = "VEDUTAOS_SAVES"        // names the card's saves folder to the dashboard, which gives each game its own

	WPASupplicant = "/bin/wpa_supplicant"                // the Wi-Fi, where the image has it: its libraries are in Libs
	Libs          = "/lib"                               // wpa_supplicant's shared libraries, and the dynamic loader
	DHCPScript    = "/etc/udhcpc.script"                 // what busybox's udhcpc runs with the address it got
	Certificates  = "/etc/ssl/certs/ca-certificates.crt" // the root certificates, where Go's HTTPS looks
)
