package config

import (
	"os"
	"path/filepath"
)

// OpenLog appends to name in the logs directory (LogsDir). Logs hold the
// library's paths and names, so the directory is the user's only (0700, set
// again when something made it wider first) and so is the file (0600).
func OpenLog(name string) (*os.File, error) {
	return openLogIn(LogsDir(), name)
}

func openLogIn(dir, name string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(dir, 0o700)
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	_ = f.Chmod(0o600)
	return f, nil
}
