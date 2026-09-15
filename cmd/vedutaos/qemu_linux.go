package main

import (
	"os/exec"
	"syscall"
)

// tieToParent makes QEMU end with this program, so that a test or a script that stops
// vedutaos does not leave an emulated machine running.
func tieToParent(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
