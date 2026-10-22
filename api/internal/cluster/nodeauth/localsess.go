package nodeauth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homeproxy"
)

// Node-local sessions (specs/epics/37-node-replica): a sign-in made on this machine
// while the home does not answer, checked against this machine's copy of the home's
// database. The copy is read only, so the session lives here: in this machine's state
// directory (by the hash of its id and of its CSRF token, never the values), valid on
// this machine only, until its own lifetime ends or the home has been back for the
// grace (the home knows nothing of it: a change through the home asks for a sign-in
// there).

// LocalSessionPrefix marks a node-local session's cookie value (a home session's is a
// UUID); the home proxy never forwards one.
const LocalSessionPrefix = homeproxy.LocalSessionPrefix

// LocalSessionsFile is the store's file in the node's state directory.
const LocalSessionsFile = "local-sessions.json"

type localSession struct {
	UserID   string    `json:"user_id"`
	CSRF     string    `json:"csrf_sha256"`
	Created  time.Time `json:"created"`
	Expires  time.Time `json:"expires"`
	Username string    `json:"username,omitempty"`
}

// LocalSessions is the node-local session store.
type LocalSessions struct {
	// Path is the file ("": memory only).
	Path string
	Now  func() time.Time

	mu   sync.Mutex
	byID map[string]localSession // by the SHA-256 hex of the session id
}

func (s *LocalSessions) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Load reads the store's file (a missing one is an empty store).
func (s *LocalSessions) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID = map[string]localSession{}
	if s.Path == "" {
		return nil
	}
	raw, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, &s.byID)
}

func (s *LocalSessions) saveLocked() error {
	if s.Path == "" {
		return nil
	}
	now := s.now()
	for k, v := range s.byID {
		if !now.Before(v.Expires) {
			delete(s.byID, k)
		}
	}
	b, err := json.Marshal(s.byID)
	if err != nil {
		return err
	}
	if old, err := os.ReadFile(s.Path); err == nil && bytes.Equal(old, b) {
		return nil
	}
	return writeFile(s.Path, b)
}

func token() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Create starts a session for a user, valid for ttl: its cookie value and CSRF token.
func (s *LocalSessions) Create(userID, username string, ttl time.Duration) (id, csrf string, expires time.Time, err error) {
	raw, err := token()
	if err != nil {
		return "", "", time.Time{}, err
	}
	csrf, err = token()
	if err != nil {
		return "", "", time.Time{}, err
	}
	id = LocalSessionPrefix + raw
	now := s.now()
	expires = now.Add(ttl)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID == nil {
		s.byID = map[string]localSession{}
	}
	s.byID[sum(id)] = localSession{UserID: userID, Username: username, CSRF: sum(csrf), Created: now, Expires: expires}
	return id, csrf, expires, s.saveLocked()
}

// Lookup is a live session's user and CSRF token hash.
func (s *LocalSessions) Lookup(id string) (userID, csrfHash string, ok bool) {
	v, ok := s.session(id)
	return v.UserID, v.CSRF, ok
}

// session is a live session as the store keeps it (when it was made, too).
func (s *LocalSessions) session(id string) (localSession, bool) {
	if !strings.HasPrefix(id, LocalSessionPrefix) {
		return localSession{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, found := s.byID[sum(id)]
	if !found || !s.now().Before(v.Expires) {
		return localSession{}, false
	}
	return v, true
}

// End ends a session (sign-out on this machine).
func (s *LocalSessions) End(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, sum(id))
	return s.saveLocked()
}

// EndUser ends every session of a user ("sign out everywhere").
func (s *LocalSessions) EndUser(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for k, v := range s.byID {
		if v.UserID == userID {
			delete(s.byID, k)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.saveLocked()
}

// Deadline shortens every session to end by t (the home is back: the grace).
func (s *LocalSessions) Deadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for k, v := range s.byID {
		if v.Expires.After(t) {
			v.Expires = t
			s.byID[k] = v
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.saveLocked()
}

// Len is the number of live sessions.
func (s *LocalSessions) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	now := s.now()
	for _, v := range s.byID {
		if now.Before(v.Expires) {
			n++
		}
	}
	return n
}
