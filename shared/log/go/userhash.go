package log

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The home's log key (Epic 53 story 53.01 §3): 32 random bytes in log.key
// beside the home's other keys (secrets.key), mode 0600, never in the database.
// A line names a person by user_h = hex(HMAC-SHA256(key, user id))[:16], never by
// the raw id, so a log bundle that leaves the home cannot name anyone; the admin
// UI turns user_h back into a name by asking the API, which hashes its own list.

// LogKeyEnv is the variable the supervisor sets to the key file's path.
const LogKeyEnv = "MAKTABA_LOG_KEY"

// LogKeyFile is the key file's name in the keys directory.
const LogKeyFile = "log.key"

// LogKeySize is the key's length in bytes.
const LogKeySize = 32

var (
	userKeyMu   sync.RWMutex
	userKey     []byte
	userKeyOnce sync.Once
)

// SetUserHashKey sets the key user ids are hashed with (tests, and processes
// that load it from somewhere other than MAKTABA_LOG_KEY). nil turns hashing off:
// a user id is then dropped from lines entirely.
func SetUserHashKey(key []byte) {
	userKeyOnce.Do(func() {})
	userKeyMu.Lock()
	userKey = append([]byte(nil), key...)
	if len(key) == 0 {
		userKey = nil
	}
	userKeyMu.Unlock()
}

func currentUserKey() []byte {
	userKeyOnce.Do(func() {
		if p := strings.TrimSpace(os.Getenv(LogKeyEnv)); p != "" {
			if k, err := ReadLogKey(p); err == nil {
				userKeyMu.Lock()
				userKey = k
				userKeyMu.Unlock()
			}
		}
	})
	userKeyMu.RLock()
	defer userKeyMu.RUnlock()
	return userKey
}

// UserHash is user_h for id under key: the first 16 hex of HMAC-SHA256.
func UserHash(key []byte, id string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(id))
	return hex.EncodeToString(m.Sum(nil))[:16]
}

// HashUser is user_h for id under this process's key; "" when there is no key
// (the id is then not written at all).
func HashUser(id string) string {
	k := currentUserKey()
	if len(k) == 0 || id == "" {
		return ""
	}
	return UserHash(k, id)
}

// ReadLogKey reads a key file; a file of the wrong size is an error.
func ReadLogKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) != LogKeySize {
		return nil, errors.New("log key has the wrong size")
	}
	return b, nil
}

// EnsureLogKey makes the key file at path when there is none (mode 0600, its
// directory 0700) and returns the key. An existing file is never replaced, even
// one of the wrong size: that is an error.
func EnsureLogKey(path string) ([]byte, error) {
	k, err := ReadLogKey(path)
	if err == nil {
		return k, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	k = make([]byte, LogKeySize)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return ReadLogKey(path) // made by another process meanwhile
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(k); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return k, nil
}
