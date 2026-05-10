// Package auth implements signed-URL JWT verification for the
// Streaming service (Story 8.1).
//
// Three claim shapes ride on top of the standard JWT envelope:
//
//	aud=streaming         sub=session_id    (manifest, segments, subs/chapters under a session)
//	aud=streaming-direct  sub=video_id      (direct play and remux)
//	aud=streaming-static  sub=<artifact-hash> (posters, sprites, thumbs, decoupled subs)
//
// The middleware never extends a token; expired tokens 401 and the
// player must call back to the API. The lib[] claim must contain the
// resource's library before any handler runs.
package auth

import (
	"errors"

	"github.com/google/uuid"
)

// AudPolicy is the per-route family. Construct one middleware per
// policy at router build time.
type AudPolicy string

const (
	AudSession AudPolicy = "streaming"
	AudDirect  AudPolicy = "streaming-direct"
	AudStatic  AudPolicy = "streaming-static"
)

// String returns the wire form of the policy (used as the JWT aud).
func (a AudPolicy) String() string { return string(a) }

// Claims is the canonical decoded payload after JWT validation. It
// mirrors the API's mint side (see api/internal/auth/jwt.Claims) but
// keeps only the subset Streaming reads — anything we don't enforce
// here is dropped at parse time so future spec drift can't smuggle in
// privileges.
type Claims struct {
	Iss     string   `json:"iss,omitempty"`
	Aud     string   `json:"aud"`
	Sub     string   `json:"sub"`
	Iat     int64    `json:"iat,omitempty"`
	Exp     int64    `json:"exp"`
	Nbf     int64    `json:"nbf,omitempty"`
	Jti     string   `json:"jti,omitempty"`
	Usr     string   `json:"usr,omitempty"`
	Lib     []string `json:"lib,omitempty"`
	IsAdmin bool     `json:"is_admin,omitempty"`
}

// LibIDs parses the lib[] claim into UUIDs. Returns an error if any
// element is not a UUID — we treat malformed tokens as missing rather
// than partially trusted.
func (c *Claims) LibIDs() ([]uuid.UUID, error) {
	if len(c.Lib) == 0 {
		return nil, errors.New("lib claim missing")
	}
	out := make([]uuid.UUID, 0, len(c.Lib))
	for _, s := range c.Lib {
		u, err := uuid.Parse(s)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

// CoversLibrary returns true if the claim's lib[] includes lib.
func (c *Claims) CoversLibrary(lib uuid.UUID) bool {
	for _, s := range c.Lib {
		if u, err := uuid.Parse(s); err == nil && u == lib {
			return true
		}
	}
	return false
}

// UserID parses the usr claim, or returns the zero UUID and an error
// if missing/malformed.
func (c *Claims) UserID() (uuid.UUID, error) {
	if c.Usr == "" {
		return uuid.Nil, errors.New("usr claim missing")
	}
	return uuid.Parse(c.Usr)
}
