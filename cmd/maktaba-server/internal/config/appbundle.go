package config

import (
	"os"
	"path/filepath"
	"strings"
)

// AppBundle is the server payload of Maktaba.app (Contents/Resources/server:
// bin/, postgres/, ffmpeg/, python/, pipeline/) when this server runs from it,
// else "". The app's launcher sets MAKTABA_APP_BUNDLE to that directory (Epic 36
// track D); a bare "1" means the payload holding this executable.
func AppBundle() string {
	v := strings.TrimSpace(os.Getenv("MAKTABA_APP_BUNDLE"))
	switch v {
	case "", "0":
		return ""
	case "1":
		exe, err := os.Executable()
		if err != nil {
			return ""
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		v = filepath.Dir(filepath.Dir(exe)) // <payload>/bin/maktaba-server
	}
	if info, err := os.Stat(v); err != nil || !info.IsDir() {
		return ""
	}
	return v
}
