package supervisor

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Safe mode (Epic 53 story 53.05): one start with only components.SafeModeComponents
// (Postgres, the API and its loops, updates, the log index; the web port is the
// supervisor's own). Turned on by the recovery console, the Mac app's tray, the "can't
// start" page, or the file <state>/state/safe-mode; the file is removed as soon as it is
// read, so the next start is normal unless asked again.

// SafeModeBoot is this start's safe mode, taken from the flag once by serve before the
// processes are built: every child is told (components.EnvSafeMode), the reconciler keeps
// all but the safe set off, /status.json says mode "safe".
var SafeModeBoot bool

// SafeModeFile is the flag's name in <state>/state.
const SafeModeFile = "safe-mode"

// SafeModePath is the flag file.
func SafeModePath(stateDir string) string { return filepath.Join(stateDir, "state", SafeModeFile) }

// RequestSafeMode writes the flag: the next start of serve is in safe mode.
func RequestSafeMode(stateDir string, now time.Time) error {
	p := SafeModePath(stateDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(now.UTC().Format(time.RFC3339)+"\n"), 0o600)
}

// CancelSafeMode removes the flag (a "leave safe mode" before the restart).
func CancelSafeMode(stateDir string) error {
	err := os.Remove(SafeModePath(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// TakeSafeMode reports whether the flag is set, and removes it: safe mode lasts one start.
func TakeSafeMode(stateDir string) bool {
	p := SafeModePath(stateDir)
	if _, err := os.Stat(p); err != nil {
		return false
	}
	_ = os.Remove(p)
	return true
}

// The recovery console's files in <state>/state (Epic 53 story 53.04).
const (
	EnvRecoveryRevoked = "MAKTABA_RECOVERY_REVOKED"
	EnvRecoveryActions = "MAKTABA_RECOVERY_ACTIONS"
)

// RecoveryRevokedPath is the API's revocation file of recovery passes.
func RecoveryRevokedPath(stateDir string) string {
	return filepath.Join(stateDir, "state", "recovery-revoked.json")
}

// RecoveryActionsPath is the console's actions, one JSON line each, for audit_log.
func RecoveryActionsPath(stateDir string) string {
	return filepath.Join(stateDir, "state", "recovery-actions.jsonl")
}
