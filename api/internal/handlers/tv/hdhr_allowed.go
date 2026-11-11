package tv

// The addresses allowed to use the tuner (Epic 57 story 57.10). The HDHomeRun
// routes and the guide exports on the streaming service cannot be signed (Plex's
// DVR and other guide software just fetch them), so they answer loopback, the Plex
// server's own address and the addresses an admin lists here, and nobody else.
//
//	GET /api/tv/hdhr/allowed   (admin) {addresses: [...]}
//	PUT /api/tv/hdhr/allowed   (admin) {addresses: [...]} → the stored list
//
// Stored in app_settings under HDHRAllowedKey as {"addresses": [...]}; the
// streaming service reads the same row on each request it must judge.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/clientip"
)

// HDHRAllowedKey is the app_settings key of the list.
const HDHRAllowedKey = "tv.hdhr_allowed"

// MaxHDHRAllowed caps the list.
const MaxHDHRAllowed = 64

// HDHRAllowed is the GET/PUT /api/tv/hdhr/allowed body: addresses or CIDRs.
type HDHRAllowed struct {
	Addresses []string `json:"addresses"`
}

// MountHDHRAllowed registers the routes.
func (h *Handler) MountHDHRAllowed(r interface {
	Get(string, http.HandlerFunc)
	Put(string, http.HandlerFunc)
}) {
	r.Get("/api/tv/hdhr/allowed", h.GetHDHRAllowed)
	r.Put("/api/tv/hdhr/allowed", h.PutHDHRAllowed)
}

func (h *Handler) loadHDHRAllowed(r *http.Request) (HDHRAllowed, error) {
	out := HDHRAllowed{Addresses: []string{}}
	var raw []byte
	err := h.DB.QueryRowContext(r.Context(), `SELECT value FROM app_settings WHERE key = $1`, HDHRAllowedKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Addresses == nil {
		return HDHRAllowed{Addresses: []string{}}, nil // an unreadable row allows nobody
	}
	return out, nil
}

// GetHDHRAllowed returns the list (admin).
func (h *Handler) GetHDHRAllowed(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	out, err := h.loadHDHRAllowed(r)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load tuner addresses").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// PutHDHRAllowed replaces the list (admin). Every entry must be an address or a
// CIDR, and never every address (a /0).
func (h *Handler) PutHDHRAllowed(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	var req HDHRAllowed
	if e := common.ReadJSON(r, &req, 16<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	clean := make([]string, 0, len(req.Addresses))
	seen := map[string]bool{}
	for _, a := range req.Addresses {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		p, err := clientip.ParseEntry(a)
		if err != nil || !p.IsValid() {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{
				Field: "addresses", Message: "\"" + a + "\" is not an address or a CIDR"}}))
			return
		}
		s := p.String()
		if p.IsSingleIP() {
			s = p.Addr().String()
		}
		if !seen[s] {
			seen[s] = true
			clean = append(clean, s)
		}
	}
	if len(clean) > MaxHDHRAllowed {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{
			Field: "addresses", Message: "at most 64 addresses"}}))
		return
	}
	raw, _ := json.Marshal(HDHRAllowed{Addresses: clean})
	var by any
	if p := principal.FromContext(r.Context()); p != nil && p.UserID != "" {
		by = p.UserID
	}
	if _, err := h.DB.ExecContext(r.Context(), `
		INSERT INTO app_settings (key, value, updated_at, updated_by) VALUES ($1, $2::jsonb, now(), $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now(), updated_by = EXCLUDED.updated_by
	`, HDHRAllowedKey, string(raw), by); err != nil {
		httperror.Write(w, r, httperror.Internal("store tuner addresses").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, HDHRAllowed{Addresses: clean})
}
