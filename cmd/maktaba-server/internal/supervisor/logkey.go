package supervisor

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// The home's log key (Epic 53 story 53.01 §3): 32 random bytes in log.key in the
// keys directory, beside secrets.key. Every role hashes user ids with it
// (user_h = HMAC-SHA256(key, id)[:16]) so no log line names a person; it is
// never in the database. Each machine keeps its own: a joined machine's user_h
// differs from the home's for the same person.

// logKeySize is the key's length (shared/log/go LogKeySize, the pipeline's LOG_KEY_SIZE).
const logKeySize = 32

// LogKeyPath is log.key in the keys directory.
func LogKeyPath() string { return filepath.Join(config.KeysDir(), "log.key") }

// ensureLogKey is EnsureLogKey (the tests keep it off this machine's own keys).
var ensureLogKey = EnsureLogKey

// EnsureLogKey makes the log key when there is none. An existing file is never
// replaced, even one of the wrong size (that is an error: the roles then log no
// user at all rather than under a second key).
func EnsureLogKey(path string) error {
	b, err := os.ReadFile(path)
	switch {
	case err == nil && len(b) == logKeySize:
		return nil
	case err == nil:
		return fmt.Errorf("log key %s: %d bytes, want %d", path, len(b), logKeySize)
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	k := make([]byte, logKeySize)
	if _, err := rand.Read(k); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeKeyFile(path, k, 0o600)
}
