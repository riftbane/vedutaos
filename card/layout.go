package card

// The card's layout, as the console reads it (cmd/vshell/init_linux.go). A card is the
// image's own volume, labelled BootLabel, or any volume labelled Label plugged into the
// console: a USB stick, or the folder QEMU shows the emulated machine as a disk. A Label
// volume is taken in preference to the boot volume.
const (
	Label       = "VEDUTA"
	BootLabel   = "VEDUTAOS"
	Dir         = "vedutaos"
	VShell      = Dir + "/vshell"        // replaces the image's dashboard while present
	EnvFile     = Dir + "/env"           // settings: VEDUTA_SCALE, VEDUTA_FB, VEDUTA_PAD
	DebugFile   = Dir + "/debug"         // when present, a shell on the serial port and on tty2
	ReleaseFile = Dir + "/release"       // which image wrote the card
	WiFiFile    = Dir + "/wifi.json"     // the Wi-Fi networks the console joined, written by the console
	KernelLog   = Dir + "/kernel.log"    // the kernel's messages, written by the console for whoever asks why
	ChannelFile = Dir + "/channel"       // the software channel updates come from: stable or beta
	UpdateFile  = Dir + "/update.tar.gz" // an update being downloaded, gone once installed
	Games       = "games"                // one folder per game
	Saves       = "saves"                // the games' saves: one folder per game, named as its folder in games
)
