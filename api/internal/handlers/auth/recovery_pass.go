package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/securityaudit"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// The recovery pass (Epic 53 story 53.04, README D4). The web's sign-in is a session
// checked in Postgres, so it cannot open the supervisor's recovery console while Postgres
// is down. While the API is up it gives each signed-in admin a pass instead: a JWT signed
// with the API's own key (the supervisor holds its public half), audience
// maktaba-recovery, 7 days, in an HttpOnly cookie scoped to /recovery. Demoting,
// deleting or signing an admin out everywhere writes their id and the time to the
// revocation file the supervisor reads, so a pass made before then is refused without
// Postgres.

const (
	// RecoveryAudience is the pass's aud.
	RecoveryAudience = "maktaba-recovery"
	// CookieRecovery is the pass's cookie (Path=/recovery).
	CookieRecovery = "maktaba_recovery"
	// RecoveryPassTTL is how long a pass lasts.
	RecoveryPassTTL = 7 * 24 * time.Hour
	// EnvRecoveryRevoked is the revocation file's path, set by the supervisor.
	EnvRecoveryRevoked = "MAKTABA_RECOVERY_REVOKED"
	// EnvRecoveryActions is the console's actions file (one JSON line each), set by
	// the supervisor: the API copies them into audit_log when it starts.
	EnvRecoveryActions = "MAKTABA_RECOVERY_ACTIONS"
)

// EventRecoveryAction is a recovery console action in audit_log.
const EventRecoveryAction securityaudit.Event = "recovery.action"

// ImportRecoveryActions copies the console's actions (written while the API may have
// been down) into audit_log, then empties the file. A line that does not parse is
// skipped; a failed write keeps the file for the next start.
func ImportRecoveryActions(ctx context.Context, w *securityaudit.Writer, path string) (int, error) {
	if w == nil || strings.TrimSpace(path) == "" {
		return 0, nil
	}
	revokeMu.Lock()
	defer revokeMu.Unlock()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var a struct {
			At     string `json:"at"`
			Action string `json:"action"`
			Detail string `json:"detail"`
			UserID string `json:"user_id"`
			Tray   bool   `json:"tray"`
		}
		if json.Unmarshal([]byte(line), &a) != nil || a.Action == "" {
			continue
		}
		by := "pass"
		if a.Tray {
			by = "tray"
		}
		if err := w.Write(ctx, securityaudit.Entry{Event: EventRecoveryAction, ActorUserID: a.UserID,
			Payload: map[string]any{"action": a.Action, "detail": a.Detail, "at": a.At, "by": by}}); err != nil {
			return n, err
		}
		n++
	}
	return n, os.Truncate(path, 0)
}

// RecoveryPass is POST /api/auth/recovery-pass: an admin gets the cookie (and the pass,
// for a native app that sends it as a bearer); a viewer 403 and no cookie; nobody 401.
func (h *Handler) RecoveryPass(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.tv/problems/unauthorized", Title: "unauthorized",
			Status: http.StatusUnauthorized, Detail: "authentication required"})
		return
	}
	if !p.IsAdmin {
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.tv/problems/forbidden", Title: "forbidden",
			Status: http.StatusForbidden, Detail: "only an admin of this home gets a recovery pass"})
		return
	}
	now := h.now()
	exp := now.Add(RecoveryPassTTL)
	tok, err := jwt.Sign(h.Keys, jwt.Claims{Iss: "maktaba", Aud: RecoveryAudience, Sub: p.UserID, Iat: now.Unix(),
		Exp: exp.Unix(), IsAdmin: true})
	if err != nil {
		httperror.Write(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieRecovery,
		Value:    tok,
		Path:     "/recovery",
		Expires:  exp,
		MaxAge:   int(RecoveryPassTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.SecureCookies,
		SameSite: http.SameSiteStrictMode,
	})
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"expires_at": exp.UTC().Format(time.RFC3339)})
}

// clearRecoveryCookie ends this browser's pass (a sign-out).
func (h *Handler) clearRecoveryCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieRecovery, Value: "", Path: "/recovery", MaxAge: -1, HttpOnly: true,
		Secure: h.SecureCookies, SameSite: http.SameSiteStrictMode})
}

// RevokedEntry is one line of the revocation file: passes of user issued before at are
// refused.
type RevokedEntry struct {
	UserID string    `json:"user_id"`
	At     time.Time `json:"at"`
}

var revokeMu sync.Mutex

// RevokeRecoveryPasses records that every pass of userID issued until now is void (a
// demotion, a deletion, a sign-out everywhere). Best effort: with no revocation file
// configured (the API outside the supervisor) there are no passes to revoke there.
func RevokeRecoveryPasses(userID string, at time.Time) error {
	path := strings.TrimSpace(os.Getenv(EnvRecoveryRevoked))
	if path == "" || userID == "" {
		return nil
	}
	revokeMu.Lock()
	defer revokeMu.Unlock()
	var list []RevokedEntry
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &list)
	}
	// one entry per user, the latest time: the file stays as small as the user list
	kept := list[:0]
	for _, e := range list {
		if e.UserID != userID {
			kept = append(kept, e)
		}
	}
	kept = append(kept, RevokedEntry{UserID: userID, At: at.UTC()})
	b, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
