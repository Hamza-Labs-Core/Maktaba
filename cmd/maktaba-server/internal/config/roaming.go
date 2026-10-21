package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// RoamingNodeConfig is this machine's node config when it is in a Windows profile
// that roams: the %APPDATA%\Maktaba\node.json of a machine that joined before Epic 38
// (it stays where it is, story 38.1) on a profile whose AppData is on a server (a
// redirected folder, a network drive) or that is a roaming user profile. Another PC
// signed in with that profile reads the same file and runs as this same machine on
// the home. "" when it is not: off Windows, with $MAKTABA_NODE_CONFIG set, a node
// config in %LOCALAPPDATA%, a profile that stays on this PC.
//
// node.json records nothing that ties it to one PC (its name is the host name at
// join, or one the owner chose), so this is a warning (RoamingNodeWarning, at every
// start), never a refusal: which of two PCs is the real node cannot be told from it.
func RoamingNodeConfig() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	return roamingNodeConfig(os.Getenv, home(), profileRoams)
}

func roamingNodeConfig(getenv func(string) string, userHome string, roams func(appdata string) bool) string {
	if strings.TrimSpace(getenv("MAKTABA_NODE_CONFIG")) != "" {
		return ""
	}
	appdata := getenv("APPDATA")
	if appdata == "" {
		appdata = filepath.Join(userHome, "AppData", "Roaming")
	}
	path := filepath.Join(appdata, "Maktaba", "node.json")
	if NodeConfigPath(getenv, "windows", userHome) != path || !roams(appdata) {
		return ""
	}
	return path
}

// RoamingNodeWarning says what RoamingNodeConfig found and how to keep the node on
// this PC.
func RoamingNodeWarning(path string) string {
	local := filepath.Join(WindowsRoot(os.Getenv, home()), "node.json")
	return "This machine's node config " + path + " is in a Windows profile that roams: another PC " +
		"signed in with this profile reads it and runs as this same machine on the home. To keep it " +
		"on this PC: stop Maktaba here, move it to " + local + ", and start Maktaba again; join any " +
		"other PC as its own machine (maktaba-pipeline join) before Maktaba runs there."
}
