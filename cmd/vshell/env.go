package main

import (
	"os"
	"strings"
)

// defaultEnv is the console's own settings; a card's vedutaos/env overrides them.
var defaultEnv = [][2]string{
	{"VEDUTA_BACKEND", "fbdev"},
	{"VEDUTA_SCALE", "1"},
	{"VEDUTAOS_GAMES", defaultGames},
}

// parseEnv reads a settings file: KEY=VALUE lines, comments and blank lines ignored,
// quotes around a value dropped.
func parseEnv(b []byte) [][2]string {
	var out [][2]string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		k = strings.TrimSpace(strings.TrimPrefix(k, "export "))
		if !ok || k == "" || strings.ContainsAny(k, " \t") {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out = append(out, [2]string{k, v})
	}
	return out
}

func setEnv(kv [][2]string) {
	for _, e := range kv {
		os.Setenv(e[0], e[1])
	}
}
