//go:build !linux

package main

// initMain is never reached: only Linux has a PID 1 to be.
func initMain() {}

func restartConsole() {}

func syncDisks() {}
