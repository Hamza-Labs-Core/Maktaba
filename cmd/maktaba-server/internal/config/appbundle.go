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

// InAppPayload reports whether this maktaba-server runs from a desktop app's
// server payload: $MAKTABA_APP_BUNDLE names one (AppBundle; the app's launcher
// sets it), or payload.json is in the folder above the bin of exe, the running
// executable with its links resolved (the app runs its setup steps with the
// payload's server directly, without the launcher).
func InAppPayload(exe string) bool {
	return AppPayload(exe) != ""
}

// AppPayload is the desktop app's server payload this maktaba-server runs from
// (InAppPayload), "" outside one: AppBundle, else the folder above the bin of
// exe when payload.json is there.
func AppPayload(exe string) string {
	if p := AppBundle(); p != "" {
		return p
	}
	if exe == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(filepath.Dir(exe))
	if _, err := os.Stat(filepath.Join(dir, "payload.json")); err != nil {
		return ""
	}
	return dir
}
