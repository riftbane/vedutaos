//go:build linux && !arm64 && !amd64

package main

// The console is arm64 and is developed on amd64; elsewhere modules cannot be loaded.
const sysFinitModule = 0
