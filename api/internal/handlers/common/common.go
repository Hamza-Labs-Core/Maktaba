// Package common holds the small handler primitives that every
// Epic 7 / Phase 6 handler package consumes: JSON read/write,
// URL-id extraction, query parsing, and the JSON envelope shape.
//
// The split exists because we want each domain handler package to be
// callable from both the REST router and the GraphQL resolvers without
// pulling in chi or net/http in the resolver path. Keeping these helpers
// in their own package gives REST handlers a single import for the
// transport scaffolding, while domain logic stays transport-agnostic.
package common

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// MaxBodyBytes is the per-request cap for JSON read by ReadJSON when
// no explicit limit is passed. Aligned with the middleware default;
// individual handlers may pass a tighter limit (e.g. settings PATCH).
const MaxBodyBytes = 1 << 20 // 1 MiB

// WriteJSON serialises v as application/json with the given status.
// Marshal errors are reported as 500 problem+json so a buggy handler
// can't accidentally ship a half-written body.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("encode response"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// WriteNoContent renders 204 with no body — used by DELETE handlers
// and by control endpoints that have no useful response payload.
func WriteNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// ReadJSON parses the request body into v using strict decoding —
// unknown fields produce a 400 invalid-json error, and bodies above
// the configured limit produce 413 body-too-large.
//
// limit ≤ 0 falls back to MaxBodyBytes.
func ReadJSON(r *http.Request, v any, limit int64) *httperror.Error {
	if limit <= 0 {
		limit = MaxBodyBytes
	}
	r.Body = http.MaxBytesReader(nil, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			return &httperror.Error{
				Type:   httperror.TypeBodyTooLarge,
				Title:  "request body too large",
				Status: http.StatusRequestEntityTooLarge,
				Detail: "body exceeds " + strconv.FormatInt(limit, 10) + " bytes",
			}
		}
		if errors.Is(err, io.EOF) {
			return &httperror.Error{
				Type:   httperror.TypeInvalidJSON,
				Title:  "invalid json",
				Status: http.StatusBadRequest,
				Detail: "request body is empty",
			}
		}
		return &httperror.Error{
			Type:   httperror.TypeInvalidJSON,
			Title:  "invalid json",
			Status: http.StatusBadRequest,
			Detail: err.Error(),
		}
	}
	// Reject trailing data so a "JSON of JSON" attack doesn't slip past
	// strict decoders that only check the first token.
	if dec.More() {
		return &httperror.Error{
			Type:   httperror.TypeInvalidJSON,
			Title:  "invalid json",
			Status: http.StatusBadRequest,
			Detail: "trailing data after JSON value",
		}
	}
	return nil
}

// QueryInt parses an int query parameter. Missing → def; unparseable
// → 400 invalid-query-parameter.
func QueryInt(r *http.Request, key string, def int) (int, *httperror.Error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, httperror.InvalidQuery(key + " must be an integer")
	}
	return n, nil
}

// QueryFloat parses a float query parameter; same semantics as QueryInt.
// NaN is rejected explicitly so a `?from=NaN` clamp-bug can't pass
// through (Story 7.6 EC-3).
func QueryFloat(r *http.Request, key string, def float64) (float64, *httperror.Error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, httperror.InvalidQuery(key + " must be a number")
	}
	if n != n { // NaN check
		return 0, httperror.InvalidQuery(key + " must be a number")
	}
	return n, nil
}

// QueryBool parses a bool query param. Accepts the case-folded strings
// "1","true","t","yes" / "0","false","f","no". Missing → def.
func QueryBool(r *http.Request, key string, def bool) (bool, *httperror.Error) {
	v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key)))
	if v == "" {
		return def, nil
	}
	switch v {
	case "1", "true", "t", "yes":
		return true, nil
	case "0", "false", "f", "no":
		return false, nil
	}
	return false, httperror.InvalidQuery(key + " must be boolean")
}

// QueryCSV reads a comma-separated query param into a deduped slice
// with whitespace trimmed. Empty value → nil (not [""]).
func QueryCSV(r *http.Request, key string) []string {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	seen := map[string]struct{}{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
