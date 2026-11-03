package components

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Capabilities serves GET /api/capabilities (story 51.09) to every signed-in user: which
// of the home's features are on. A viewer (and a cloud play sign-in) gets the features
// only; an admin (and an admin's cloud manage link) also the admin block, which names
// the components behind each feature that is off and why. No sign-in is 401.
//
// The answer is the holder's in-memory snapshot (no query), with its ETag: a matching
// If-None-Match is 304.
type Capabilities struct {
	Caps *svc.Capabilities
}

// Mount wires the route.
func (h *Capabilities) Mount(r chi.Router) {
	r.Get("/api/capabilities", h.get)
}

func (h *Capabilities) get(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, problem(http.StatusUnauthorized, "auth_required", "sign in", "sign in to read the home's features"))
		return
	}
	s := h.Caps.Snapshot()
	tag := `"` + s.ETag() + `"`
	hd := w.Header()
	hd.Set("Cache-Control", "private, no-cache")
	hd.Set("ETag", tag)
	hd.Set("Vary", "Authorization, Cookie")
	if etagMatches(r.Header.Get("If-None-Match"), s.ETag()) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	hd.Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.Body(p.IsAdmin))
}

// etagMatches reports whether an If-None-Match header names etag (quoted or not, weak
// or strong, one of a list, or *).
func etagMatches(header, etag string) bool {
	if header == "" {
		return false
	}
	for _, v := range strings.Split(header, ",") {
		v = strings.TrimSpace(v)
		if v == "*" {
			return true
		}
		v = strings.TrimPrefix(v, "W/")
		if strings.Trim(v, `"`) == etag {
			return true
		}
	}
	return false
}
