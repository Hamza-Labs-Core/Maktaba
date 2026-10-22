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
	return OpenLogFile(filepath.Join(dir, name))
}

// OpenLogFile appends to the log at path, the user's only (0600): a folder it
// makes is 0700; one that is there is left as it is (it may not be Maktaba's).
func OpenLogFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	_ = f.Chmod(0o600)
	return f, nil
}
