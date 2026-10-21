package nodeauth

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// A node keeps the home's last sign-in answers on its disk (PR #145), so one that
// restarts in a home outage still serves, under the same offline rules, the people the
// home confirmed: keyed by the credential's hash (never the session id or token
// itself), with the principal, the CSRF token's hash, the credential's expiry and when
// the home confirmed it, and when the home last answered at all (the outage's start
// for a process that never saw the home). A refusal is not kept; a revocation the home
// pushed removes its entries at the next save.

// AnswersFile is the file in the node's state directory.
const AnswersFile = "auth-answers.json.gz"

// keepEvery is how often changed answers are written.
const keepEvery = 30 * time.Second

const keptVersion = 1

type keptFile struct {
	Version int                   `json:"version"`
	LastOK  time.Time             `json:"last_ok"`
	Answers map[string]keptAnswer `json:"answers"`
}

type keptAnswer struct {
	UserID             string    `json:"user_id"`
	Username           string    `json:"username,omitempty"`
	IsAdmin            bool      `json:"is_admin,omitempty"`
	Libraries          []string  `json:"libraries,omitempty"`
	AccessAllLibraries bool      `json:"access_all_libraries,omitempty"`
	Source             string    `json:"source"`
	MustChangePassword bool      `json:"must_change_password,omitempty"`
	CSRF               string    `json:"csrf_sha256,omitempty"`
	Confirmed          time.Time `json:"confirmed"`
	Expires            time.Time `json:"expires"`
}

// LoadKept puts the answers kept at path into the cache (before the node serves): a
// missing file is none, an unreadable one is logged and ignored.
func (r *Resolver) LoadKept(path string) {
	if err := r.loadKept(path); err != nil && !errors.Is(err, os.ErrNotExist) && r.Logger != nil {
		r.Logger.Warn("node: the kept sign-in answers are unreadable; starting without them",
			"path", path, "err", err, "event", "node_auth_kept_unreadable")
	}
}

// Keep writes the answers to path whenever they changed (at most every 30 s, and once
// more when ctx ends).
func (r *Resolver) Keep(ctx context.Context, path string) {
	t := time.NewTicker(keepEvery)
	defer t.Stop()
	var last []byte
	save := func() {
		b, err := r.keptBytes()
		if err != nil || bytes.Equal(b, last) {
			return
		}
		if err := writeKept(path, b); err != nil {
			if r.Logger != nil {
				r.Logger.Warn("node: the sign-in answers were not kept", "err", err, "event", "node_auth_keep_failed")
			}
			return
		}
		last = b
	}
	for {
		select {
		case <-ctx.Done():
			save()
			return
		case <-t.C:
			save()
		}
	}
}

func (r *Resolver) loadKept(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	var f keptFile
	if err := json.NewDecoder(zr).Decode(&f); err != nil {
		return err
	}
	if f.Version != keptVersion {
		return errors.New("kept answers of another version")
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = map[string]*entry{}
	}
	if r.lastOK.IsZero() {
		r.lastOK = f.LastOK
	}
	for key, a := range f.Answers {
		if _, ok := r.cache[key]; ok || !now.Before(a.Expires) {
			continue // a fresher answer, or one that has expired
		}
		src := principal.Source(a.Source)
		r.cache[key] = &entry{
			p: &principal.Principal{UserID: a.UserID, IsAdmin: a.IsAdmin, Libraries: a.Libraries,
				AccessAllLibraries: a.AccessAllLibraries, Source: src, MustChangePassword: a.MustChangePassword},
			csrf: a.CSRF, user: a.UserID, name: a.Username, confirmed: a.Confirmed, expires: a.Expires,
			// until zero: the home is asked first whenever it answers
		}
	}
	return nil
}

// keptBytes is the cache as kept: the answers that could still serve offline.
func (r *Resolver) keptBytes() ([]byte, error) {
	now := r.now()
	r.mu.Lock()
	f := keptFile{Version: keptVersion, LastOK: r.lastOK, Answers: map[string]keptAnswer{}}
	for key, e := range r.cache {
		if e.denied || e.p == nil || !now.Before(e.expires) {
			continue
		}
		f.Answers[key] = keptAnswer{UserID: e.p.UserID, Username: e.name, IsAdmin: e.p.IsAdmin,
			Libraries: e.p.Libraries, AccessAllLibraries: e.p.AccessAllLibraries, Source: string(e.p.Source),
			MustChangePassword: e.p.MustChangePassword, CSRF: e.csrf, Confirmed: e.confirmed, Expires: e.expires}
	}
	r.mu.Unlock()
	return json.Marshal(f)
}

func writeKept(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".answers-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	zw := gzip.NewWriter(tmp)
	if _, err := zw.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
