package supervisor

// A TV held on the streaming software it ran (story 35.6, the owner's decision of
// 2026-10-28: "when only the TV test fails, keep the update installed, leave TV on the old
// version"). An update restarts the TV slots with the rest of the server (the desktop
// apps always; a plain server when its slots cannot be handed over): a slot the front door
// has not bound to a release would start on the newly installed streaming binary, so the
// live TV would be on the new software before its change was ever checked, and stay there
// when the check fails. The supervisor asks, at its slots' first start, which streaming
// software the live TV runs (the live slot's own report in tv_slots); while that is an
// older release than this one, and the TV moves to new streaming software only through a
// checked change (staging on, a TV release on the air that does not record this one's),
// every slot the front door has not bound runs that release's binary: from
// bin/releases/<v>, or where the install keeps it (WithKeptStreaming). It stays there
// until a change swaps it (the front door binds the slots to the new release: a Retry that
// passes, a later release's change), across every restart in between (a Drop). A held
// release whose binary is nowhere is said loudly, and the slots run this release's.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	_ "github.com/lib/pq" // the home's Postgres (the held TV's query)

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// EventTVHeldMissing: the live TV is held on a release whose streaming binary this install
// no longer has; the slots run the current one.
const EventTVHeldMissing = "tv_held_binary_missing"

// heldQueryTimeout bounds the held TV's query at the slots' first start.
const heldQueryTimeout = 5 * time.Second

// heldStreaming is what the slots run while the TV is held: resolved once, at the first
// slot start.
type heldStreaming struct {
	resolved bool
	version  string // the release the live TV is held on ("" when it is not)
	path     string // its streaming binary ("" when not held, or missing)
}

// HeldStreamingVersion is the streaming software the live TV is held on: the version the
// live slot last reported (tv_slots.version, the binary's own), when it is an older
// release than current and a checked change is the only way the TV moves to current's
// (rules.tv_staging.enabled, a TV release on the air that does not record current's
// streaming). "" when the TV is not held: staging off (the restart puts the new software
// on), no TV release yet, the live release recording current's binary already, no live
// slot yet, or one on current's software or a newer one (a launcher's fallback: the slots
// run the release the launcher runs). current not a release (a source build): "".
func HeldStreamingVersion(ctx context.Context, db *sql.DB, current string) (string, error) {
	if !isRelease(current) {
		return "", nil
	}
	var raw sql.NullString
	err := db.QueryRowContext(ctx, `SELECT value::text FROM app_settings WHERE key = 'rules.tv_staging'`).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if stagingOff(raw.String) {
		return "", nil
	}
	var relVer sql.NullString
	err = db.QueryRowContext(ctx, `SELECT r.streaming_version FROM tv_routing t
		JOIN tv_releases r ON r.id = t.live_release_id WHERE t.scope = 'cluster'`).Scan(&relVer)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", err
	case sameRelease(relVer.String, current):
		return "", nil
	}
	var live sql.NullString
	err = db.QueryRowContext(ctx, `SELECT version FROM tv_slots WHERE role = 'live'
		AND COALESCE(version, '') <> '' ORDER BY beat_at DESC NULLS LAST LIMIT 1`).Scan(&live)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", err
	}
	v := strings.TrimSpace(live.String)
	if !isRelease(v) || layout.CompareVersions(v, current) >= 0 {
		return "", nil
	}
	return v, nil
}

// heldQuery is the held TV's query on the home's database at dsn (nil: not a Postgres).
func heldQuery(dsn string) func(ctx context.Context) (string, error) {
	u := strings.ToLower(strings.TrimSpace(dsn))
	if !strings.HasPrefix(u, "postgres://") && !strings.HasPrefix(u, "postgresql://") {
		return nil
	}
	return func(ctx context.Context) (string, error) {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			return "", err
		}
		defer db.Close()
		ctx, cancel := context.WithTimeout(ctx, heldQueryTimeout)
		defer cancel()
		return HeldStreamingVersion(ctx, db, version.Version)
	}
}

// isRelease: a version with a numeric core (not "dev", a branch's name).
func isRelease(v string) bool {
	return strings.TrimSpace(v) != "" && layout.CompareVersions(v, "0.0.0") > 0
}

func sameRelease(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	return a != "" && b != "" && layout.CompareVersions(a, b) == 0
}

// stagingOff: rules.tv_staging says enabled = false (the default is on).
func stagingOff(raw string) bool {
	var m map[string]json.RawMessage
	if raw == "" || json.Unmarshal([]byte(raw), &m) != nil {
		return false
	}
	var on bool
	if v, ok := m["enabled"]; ok && json.Unmarshal(v, &on) == nil {
		return !on
	}
	return false
}

// heldFor resolves, once, the streaming binary the slots the front door has not bound run
// while the TV is held (c's Process.Held asks): its version and path. Called outside s.mu.
func (s *Supervisor) heldFor(ctx context.Context, c *child) heldStreaming {
	s.mu.Lock()
	h := s.held
	s.mu.Unlock()
	if h.resolved || c.proc.Held == nil {
		return h
	}
	s.heldMu.Lock()
	defer s.heldMu.Unlock()
	s.mu.Lock()
	h = s.held
	s.mu.Unlock()
	if h.resolved {
		return h
	}
	h = heldStreaming{resolved: true}
	v, err := c.proc.Held(ctx)
	switch {
	case err != nil:
		s.warnf("which streaming software the live TV runs could not be read (%v): the TV slots start on this release's", err)
	case v != "":
		h.version = v
		rb, rerr := resolveReleaseBinary(releaseBinRoot(c.basePath), v, "", version.Version, c.basePath, s.keptStreaming)
		if rerr != nil {
			msg := fmt.Sprintf("the live TV is held on streaming %s (its update's TV check failed, or the change was dropped), "+
				"but that release's streaming binary is not kept on this machine (%v): the TV slots run this release's (%s) "+
				"instead, so the live TV is on the new streaming software", v, rerr, version.Version)
			s.errorf("%s", msg)
			s.emit(Event{Role: RoleTVSlot, Kind: EventTVHeldMissing, Message: msg})
			break
		}
		h.path = rb.Path
		s.logf("the live TV is held on streaming %s: the TV slots the front door has not bound run %s until a TV change swaps them",
			v, rb.Path)
	}
	s.mu.Lock()
	s.held = h
	s.mu.Unlock()
	return h
}

// applyHeld points an unbound slot's command at the held release's binary (no
// MAKTABA_TV_RELEASE: it binds itself to the live release, as any unbound slot).
func applyHeld(cmd *exec.Cmd, path string) {
	cmd.Path = path
	if len(cmd.Args) > 0 {
		cmd.Args[0] = path
	}
	env := make([]string, 0, len(cmd.Env))
	for _, e := range cmd.Env {
		if !strings.HasPrefix(e, "MAKTABA_TV_RELEASE=") {
			env = append(env, e)
		}
	}
	cmd.Env = env
}

// HeldStreaming is the release the live TV is held on and the binary the unbound slots run
// for it ("", "" when it is not held, or before the slots' first start).
func (s *Supervisor) HeldStreaming() (version, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held.version, s.held.path
}
