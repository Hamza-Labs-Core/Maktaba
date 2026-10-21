package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// CacheDir is where the install keeps what it can make again: the streaming
// role's segments and transcodes, pip's downloads when the venv is built. On
// Linux, when the server runs from the Linux app's payload with the default
// data directory, that is $XDG_CACHE_HOME/maktaba (Epic 39 story 39.4:
// ~/.cache, which backups skip); everywhere else, and for a data_dir the owner
// chose, <state>/cache, where it always was.
func (c Config) CacheDir() string {
	return cacheDir(runtime.GOOS, os.Getenv, home(), c.StateDir(), c.Server.DataDir, LocalDirs().Data, AppBundle() != "")
}

func cacheDir(goos string, getenv func(string) string, userHome, stateDir, dataDir, defaultData string, inApp bool) string {
	if goos == "linux" && inApp && filepath.Clean(dataDir) == filepath.Clean(defaultData) {
		base := getenv("XDG_CACHE_HOME")
		if base == "" {
			base = filepath.Join(userHome, ".cache")
		}
		return filepath.Join(base, "maktaba")
	}
	return filepath.Join(stateDir, "cache")
}
