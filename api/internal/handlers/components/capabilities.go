package components

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
//
// With Parental set the answer also carries the caller's own `parental` block (Epic 57
// story 57.12), and the ETag covers it, so a changed limit is a changed answer. When
// the block cannot be read the route answers 503: an app must not take a missing block
// for an account nothing limits.
type Capabilities struct {
	Caps *svc.Capabilities
	// Parental answers the caller's `parental` block (nil: none is sent).
	Parental func(r *http.Request, p *principal.Principal) (any, error)
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
	etag, body := s.ETag(), s.Body(p.IsAdmin)
	if h.Parental != nil {
		block, err := h.Parental(r, p)
		var raw []byte
		if err == nil {
			raw, err = json.Marshal(block)
		}
		if err != nil {
			w.Header().Set("Retry-After", "5")
			httperror.Write(w, r, problem(http.StatusServiceUnavailable, "parental_unavailable", "try again",
				"the account's limits cannot be read now").Wrap(err))
			return
		}
		sum := sha256.Sum256(raw)
		etag += "-" + hex.EncodeToString(sum[:6])
		body = withParental(body, raw)
	}
	hd := w.Header()
	hd.Set("Cache-Control", "private, no-cache")
	hd.Set("ETag", `"`+etag+`"`)
	hd.Set("Vary", "Authorization, Cookie")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	hd.Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// withParental is the snapshot's JSON object with the caller's `parental` block as its
// last member (the snapshot is shared and is not changed).
func withParental(body, block []byte) []byte {
	trimmed := bytes.TrimRight(body, " \t\r\n")
	if len(trimmed) < 2 || trimmed[len(trimmed)-1] != '}' {
		return body
	}
	// grown by append, never sized from the two lengths (their sum could overflow)
	out := bytes.Clone(trimmed[:len(trimmed)-1])
	if len(bytes.TrimSpace(trimmed[1:len(trimmed)-1])) > 0 {
		out = append(out, ',')
	}
	out = append(out, `"parental":`...)
	out = append(out, block...)
	return append(out, '}')
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
