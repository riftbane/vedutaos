package card

// The card's layout, as the console reads it (see image/rootfs/usr/lib/vedutaos). A card
// is the boot partition of a console, or any volume labelled Label plugged into one: a USB
// stick, or the folder QEMU shows the emulated machine as a disk.
const (
	Label    = "VEDUTA"
	Dir      = "vedutaos"
	VShell   = Dir + "/vshell"          // replaces the image's dashboard while present
	EnvFile  = Dir + "/env"             // settings overriding /etc/default/vedutaos
	KeysFile = Dir + "/authorized_keys" // public keys admitted as the user veduta over ssh
	Games    = "games"                  // one folder per game
)
