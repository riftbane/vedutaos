//go:build !linux

package main

import "os/exec"

func tieToParent(*exec.Cmd) {}
