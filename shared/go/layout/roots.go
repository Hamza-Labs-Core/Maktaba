package layout

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// ErrLocked is returned by Lock when another process holds the install lock.
var ErrLocked = errors.New("already_running")

// Mode is how the OS service runs Maktaba (design §2.3).
type Mode string

const (
	// ModeSystem: a system unit / LaunchDaemon / Windows service.
	ModeSystem Mode = "system"
	// ModeUser: a user unit / LaunchAgent / Windows logon task (the Windows
	// default: mapped drives are per user).
	ModeUser Mode = "user"
)

// DefaultRoot is the install root for an OS and mode (design §2.1).
func DefaultRoot(mode Mode) string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		if mode == ModeSystem {
			if pd := os.Getenv("ProgramData"); pd != "" {
				return filepath.Join(pd, "Maktaba")
			}
			return `C:\ProgramData\Maktaba`
		}
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			return filepath.Join(la, "Maktaba")
		}
		return filepath.Join(home, "AppData", "Local", "Maktaba")
	case "darwin":
		if mode == ModeSystem {
			return "/usr/local/var/maktaba"
		}
		return filepath.Join(home, "Library", "Application Support", "Maktaba", "server")
	default:
		if mode == ModeSystem {
			return "/var/lib/maktaba"
		}
		if x := os.Getenv("XDG_DATA_HOME"); x != "" {
			return filepath.Join(x, "maktaba")
		}
		return filepath.Join(home, ".local", "share", "maktaba")
	}
}
