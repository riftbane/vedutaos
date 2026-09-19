//go:build !linux

package main

import (
	"errors"
	"time"
)

// initMain is never reached: only Linux has a PID 1 to be.
func initMain() {}

func restartConsole() {}

func syncDisks() {}

func modprobe([]string) int { return 1 }

func saveKernelLog() {}

func wifiDiagnosis() string { return "" }

func setSystemClock(time.Time) error { return errors.New("only the console sets its clock") }
