package nodeauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homelink"
)

// Sign-outs made on this machine while the home does not answer (#145): a person who
// signs out of a home session here is signed out here at once (the cookies cleared,
// the session denied on this machine, its cached answer dropped), and the home is told
// as soon as it answers again (Link.SignOut), so the session ends everywhere. "Sign out
// everywhere" does the same for every session of the user made before it. The store
// keeps them in the node's state directory, by the SHA-256 of the session's id (never
// the id: the file never holds a credential), saved before the answer, until the home
// took them and a while after (this machine's copy may not have the revocation yet).
// Only a session this machine knows is kept (its copy has it, or the home answered for
// it here: any cookie at all would fill the store), at most maxSignOuts of them, and
// they are handed over signOutBatch at a time, within what the home takes in one
// request, so one the home refuses holds up none of the others.

// SignOutsFile is the store's file in the node's state directory.
const SignOutsFile = "signed-out.json"

// LogoutAllPath is "sign out everywhere".
const LogoutAllPath = "/api/auth/logout-all"

// signOutKeep is how long a sign-out the home took is still denied here: this
// machine's copy applies the home's revocation within moments; a copy further behind
// than that does not answer anyway (rules.cluster.offline_copy_max_lag_minutes).
const signOutKeep = time.Hour

// signOutMaxAge bounds a sign-out the home never took (a session ends by then).
const signOutMaxAge = 30 * 24 * time.Hour

// maxSignOuts caps the store. Each is a session this machine knows, signed out here,
// so the cap is far past what an outage makes; once it is reached the ones the home
// took go first, and a sign-out past it waits for the home.
const maxSignOuts = 4096

// signOutBatch is how many sessions one hand-over names: the home takes at most 1000
// sessions and 64 KiB a request, and a session's hash is 67 bytes of JSON.
const signOutBatch = 500

// errSignOutsFull: the store holds maxSignOuts the home has not taken yet.
var errSignOutsFull = errors.New("nodeauth: too many sign-outs wait for the home")

type signOut struct {
	// User is the session's user; with All, every session of the user made before At.
	User string    `json:"user_id,omitempty"`
	All  bool      `json:"all,omitempty"`
	At   time.Time `json:"at"`
	// Sent is when the home took it (zero: not yet).
	Sent time.Time `json:"sent,omitempty"`
}

// SignOuts is the store of the sign-outs made on this machine.
type SignOuts struct {
	// Path is the file ("": memory only).
	Path string
	Now  func() time.Time

	mu    sync.Mutex
	byKey map[string]signOut // a session's SHA-256 hex, or "u:"+user for every session of a user
}

func (s *SignOuts) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Load reads the store's file (a missing one is an empty store).
func (s *SignOuts) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byKey = map[string]signOut{}
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
	return json.Unmarshal(raw, &s.byKey)
}

// pruneLocked lets go of what is past its time (held lock).
func (s *SignOuts) pruneLocked() {
	now := s.now()
	for k, v := range s.byKey {
		if (!v.Sent.IsZero() && now.Sub(v.Sent) > signOutKeep) || now.Sub(v.At) > signOutMaxAge {
			delete(s.byKey, k)
		}
	}
}

func (s *SignOuts) saveLocked() error {
	s.pruneLocked()
	if s.Path == "" {
		return nil
	}
	b, err := json.Marshal(s.byKey)
	if err != nil {
		return err
	}
	return writeFile(s.Path, b)
}

// add records a sign-out (saved before it returns); errSignOutsFull when the store
// holds maxSignOuts the home has not taken.
func (s *SignOuts) add(key string, v signOut) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byKey == nil {
		s.byKey = map[string]signOut{}
	}
	if _, ok := s.byKey[key]; !ok && len(s.byKey) >= maxSignOuts {
		s.pruneLocked()
		for k, was := range s.byKey {
			// the home has these: its own revocation, pushed to every machine, holds
			// them on this machine's copy
			if len(s.byKey) < maxSignOuts {
				break
			}
			if !was.Sent.IsZero() {
				delete(s.byKey, k)
			}
		}
		if len(s.byKey) >= maxSignOuts {
			return errSignOutsFull
		}
	}
	s.byKey[key] = v
	return s.saveLocked()
}

// denied reports whether a home session (by its key, of user) was signed out here.
func (s *SignOuts) denied(key, user string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byKey[key]; ok {
		return true
	}
	// every session of the user, until the home took it: then a session the home says
	// is live was made after it (the home ended the older ones)
	v, ok := s.byKey["u:"+user]
	return ok && user != "" && v.Sent.IsZero()
}

// anyAll reports whether a "sign out everywhere" waits for the home (a request's user
// must then be known to tell whether it is denied).
func (s *SignOuts) anyAll() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.byKey {
		if v.All && v.Sent.IsZero() {
			return true
		}
	}
	return false
}

// pending is what the home has not taken yet, as requests to it: the sessions
// signOutBatch at a time, then each "sign out everywhere" on its own.
func (s *SignOuts) pending() []delegation.SignOutRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sessions, users []string
	for k, v := range s.byKey {
		switch {
		case !v.Sent.IsZero():
		case v.All:
			users = append(users, k)
		default:
			sessions = append(sessions, k)
		}
	}
	sort.Strings(sessions)
	sort.Strings(users)
	var out []delegation.SignOutRequest
	for len(sessions) > 0 {
		n := min(len(sessions), signOutBatch)
		out = append(out, delegation.SignOutRequest{SessionSHA256: sessions[:n:n]})
		sessions = sessions[n:]
	}
	for _, k := range users {
		v := s.byKey[k]
		at := v.At
		out = append(out, delegation.SignOutRequest{UserID: v.User, Before: &at})
	}
	return out
}

// sent marks what a request carried as taken by the home.
func (s *SignOuts) sent(req delegation.SignOutRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	mark := func(k string) {
		if v, ok := s.byKey[k]; ok && v.Sent.IsZero() {
			v.Sent = now
			s.byKey[k] = v
		}
	}
	for _, k := range req.SessionSHA256 {
		mark(k)
	}
	if req.UserID != "" && req.Before != nil {
		mark("u:" + req.UserID)
	}
	return s.saveLocked()
}

// Waiting is how many sign-outs wait for the home.
func (s *SignOuts) Waiting() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, v := range s.byKey {
		if v.Sent.IsZero() {
			n++
		}
	}
	return n
}

// deniedHere reports whether the request's home session was signed out on this
// machine (user: its user when known).
func (r *Resolver) deniedHere(req *http.Request, user string) bool {
	if r.SignOuts == nil {
		return false
	}
	c, err := req.Cookie(CookieSession)
	if err != nil || c.Value == "" || bearer(req) != "" {
		return false
	}
	return r.SignOuts.denied(SessionKey(c.Value), user)
}

// SignOutHere answers a sign-out of a home session (POST /api/auth/logout, or
// /api/auth/logout-all) the home cannot take now: this machine signs the person out
// itself and tells the home when it answers again (see above). False: the request is
// another one, or this machine keeps no sign-outs.
func (r *Resolver) SignOutHere(w http.ResponseWriter, req *http.Request) bool {
	all := req.URL.Path == LogoutAllPath
	if (req.URL.Path != LogoutPath && !all) || req.Method != http.MethodPost || r.SignOuts == nil || bearer(req) != "" {
		return false
	}
	c, err := req.Cookie(CookieSession)
	if err != nil || c.Value == "" {
		return false
	}
	w.Header().Set(HomeStateHeader, "unreachable")
	key := SessionKey(c.Value)
	user, csrf, known := r.sessionHere(req.Context(), c.Value)
	if all && !known {
		// whose sessions to end is not known here: that waits for the home
		writeUnreachable(w, true)
		return true
	}
	if !csrfOK(req, csrf) {
		writeProblem(w, http.StatusForbidden, "https://maktaba.dev/problems/csrf-mismatch", "csrf token mismatch", "")
		return true
	}
	if !known {
		// a session this machine neither has on its copy nor was answered for: there is
		// nothing here to sign out, and nothing is kept for it (any cookie at all would
		// fill the store); this browser forgets it
		if r.Logger != nil {
			r.Logger.Info("node: a sign-out of a session this machine does not know; its cookies cleared, nothing kept",
				"event", "node_sign_out_unknown")
		}
		r.setCookies(w, "", "", time.Time{})
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	// on the home's clock (the sessions it ends are the ones it opened before then)
	now := r.homeNow()
	err = r.SignOuts.add(key, signOut{User: user, At: now})
	if err == nil && all {
		err = r.SignOuts.add("u:"+user, signOut{User: user, All: true, At: now})
	}
	switch {
	case errors.Is(err, errSignOutsFull):
		if r.Logger != nil {
			r.Logger.Warn("node: a sign-out made here waits for the home: too many wait already", "max", maxSignOuts,
				"event", "node_sign_outs_full")
		}
		writeUnreachable(w, true)
		return true
	case err != nil:
		if r.Logger != nil {
			r.Logger.Warn("node: a sign-out made here was not saved", "err", err, "event", "node_sign_out_failed")
		}
		writeProblem(w, http.StatusInternalServerError, "https://maktaba.dev/problems/internal", "sign-out failed", "")
		return true
	}
	rv := delegation.Revoked{SessionSHA256: []string{key}}
	if all {
		rv.UserID = user
		if r.Sessions != nil {
			_ = r.Sessions.EndUser(user)
		}
	}
	r.drop(rv)
	if r.Logger != nil {
		r.Logger.Info("node: signed out on this machine; the home is told when it answers", "user_id", user,
			"everywhere", all, "event", "node_sign_out_here")
	}
	r.setCookies(w, "", "", time.Time{})
	w.WriteHeader(http.StatusNoContent)
	return true
}

// sessionHere is a home session as this machine knows it: whose it is and its CSRF
// token's hash, from its copy (current or not: a copy behind still has the sessions it
// had, by their hash), else from the home's last answer kept here. False: one it does
// not know (the copy does not have it as live, and the home never answered for it here).
func (r *Resolver) sessionHere(ctx context.Context, sid string) (user, csrf string, ok bool) {
	if r.Local != nil {
		lctx, cancel := context.WithTimeout(ctx, localTimeout)
		a, err := r.Local.Introspect(lctx, delegation.IntrospectRequest{Session: sid})
		cancel()
		if err == nil && (a.ExpiresAt.IsZero() || r.now().Before(a.ExpiresAt)) {
			return a.Principal.UserID, a.CSRFSHA256, true
		}
	}
	if e, ok := r.cached(SessionKey(sid)); ok && !e.denied {
		return e.user, e.csrf, true
	}
	return "", "", false
}

// homeNow is the home's time now (HomeOffset): a sign-out made here is stamped on it,
// since the home ends the sessions it opened before then.
func (r *Resolver) homeNow() time.Time { return r.onHomeClock(r.now()) }

// csrfOK is the double-submit check of a sign-out answered here: the header equals the
// cookie and, when the session's CSRF token hash is known, hashes to it.
func csrfOK(req *http.Request, want string) bool {
	header := req.Header.Get(CSRFHeader)
	c, err := req.Cookie(CookieCSRF)
	if header == "" || err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(header)) != 1 {
		return false
	}
	if want == "" {
		return true
	}
	sum := sha256.Sum256([]byte(header))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(want)) == 1
}

// sendSignOuts hands the home what was signed out here while it did not answer (at
// each answer Watch gets, while any wait), a request at a time (pending). The home not
// answering, or not taking this machine's word now, stops the round (the next answer
// tries again); a request the home refuses is tried again then, and the others go on.
func (r *Resolver) sendSignOuts(ctx context.Context) {
	if r.SignOuts == nil || r.SignOuts.Waiting() == 0 {
		return
	}
	for _, req := range r.SignOuts.pending() {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		a, err := r.Link.SignOut(cctx, req)
		cancel()
		if err != nil {
			stop := ctx.Err() != nil || errors.Is(err, homelink.ErrUnreachable) || errors.Is(err, homelink.ErrUnauthorized)
			if r.Logger != nil {
				r.Logger.Warn("node: the home did not take sign-outs made here; tried again", "err", err,
					"sessions", len(req.SessionSHA256), "user_id", req.UserID, "event", "node_sign_outs_not_sent")
			}
			if stop {
				return
			}
			continue
		}
		if err := r.SignOuts.sent(req); err != nil && r.Logger != nil {
			r.Logger.Warn("node: the sign-outs the home took were not saved", "err", err, "event", "node_sign_out_failed")
		}
		if r.Logger != nil {
			r.Logger.Info("node: the home took the sign-outs made here", "sessions", len(req.SessionSHA256),
				"user_id", req.UserID, "revoked", a.Revoked, "event", "node_sign_outs_sent")
		}
	}
}
