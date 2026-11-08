package symbols

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Deps are the symbol routes:
//
//	POST /v1/symbols          a table (Encode's form), with the symbols token (?version=): a
//	                          dSYM's, or an Android release's R8 mapping (kind "r8")
//	GET  /v1/admin/symbols    the kept tables, for the service admin
type Deps struct {
	Store *Store
	// Token is the uploaders' bearer token (MAKTABA_CLOUD_SYMBOLS_TOKEN_FILE); empty: uploads
	// are off
	Token  string
	Logger *slog.Logger
	// Gate admits service admins only; nil refuses everyone
	Gate func(http.Handler) http.Handler
}

// MountUpload registers the upload route (its own token, no account).
func MountUpload(r chi.Router, d *Deps) { r.Post("/v1/symbols", d.Upload) }

// MountAdmin registers the list behind the service admin's gate.
func MountAdmin(r chi.Router, d *Deps) {
	gate := d.Gate
	if gate == nil {
		gate = func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeErr(w, http.StatusForbidden, "admin_access_denied", "")
			})
		}
	}
	r.With(gate).Get("/v1/admin/symbols", d.List)
}

func (d *Deps) authorized(r *http.Request) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && d.Token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(d.Token)) == 1
}

// Upload keeps one table.
func (d *Deps) Upload(w http.ResponseWriter, r *http.Request) {
	if d.Token == "" {
		writeErr(w, http.StatusServiceUnavailable, "symbols_off", "no symbols token is set up")
		return
	}
	if !d.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="maktaba-symbols"`)
		writeErr(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxTable))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "a symbol table is at most 64 MB")
		return
	}
	t, err := Decode(b)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_table", err.Error())
		return
	}
	version := r.URL.Query().Get("version")
	if t.Kind == KindR8 {
		// a mapping is its release's: the version it is keyed by is the one listed
		version = t.Release
	}
	if err := d.Store.Put(r.Context(), t, b, version); err != nil {
		if d.Logger != nil {
			d.Logger.Error("symbols: put", "err", err)
		}
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	if d.Logger != nil {
		d.Logger.Info("symbol table uploaded", "debug_id", t.DebugID, "code_file", t.CodeFile, "arch", t.Arch, "kind", t.Kind,
			"size", len(b), "event", "symbol_table_uploaded")
	}
	writeJSON(w, http.StatusCreated, map[string]any{"debug_id": t.DebugID, "code_file": t.CodeFile, "arch": t.Arch,
		"kind": t.Kind, "functions": len(t.Funcs), "classes": len(t.Classes)})
}

// List is the kept tables.
func (d *Deps) List(w http.ResponseWriter, r *http.Request) {
	list, err := d.Store.List(r.Context())
	if err != nil {
		if d.Logger != nil {
			d.Logger.Error("symbols: list", "err", err)
		}
		writeErr(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tables": list})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]string{"error": kind, "message": msg})
}
