package serverkeys

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"
)

// JWKSEntry mirrors the JSON shape served by the local
// `/api/.well-known/server-identity.json` endpoint.
type JWKSEntry struct {
	Kid          string `json:"kid"`
	Alg          string `json:"alg"`
	PublicKeyB64 string `json:"public_key_b64"`
	CreatedAt    string `json:"created_at"`
	RetiresAt    string `json:"retires_at,omitempty"`
}

// JWKSResponse is the body of the server-identity JWKS endpoint.
type JWKSResponse struct {
	Active  JWKSEntry   `json:"active"`
	Overlap []JWKSEntry `json:"overlap,omitempty"`
}

// JWKS builds the JWKS payload from the store's current state.
func (s *Store) JWKS() JWKSResponse {
	active := s.Active()
	out := JWKSResponse{
		Active: JWKSEntry{
			Kid:          active.Kid,
			Alg:          "EdDSA",
			PublicKeyB64: base64.StdEncoding.EncodeToString(active.PublicKey),
			CreatedAt:    active.CreatedAt.UTC().Format(time.RFC3339),
		},
	}
	if prev, retiresAt, ok := s.OverlapKey(); ok {
		out.Overlap = append(out.Overlap, JWKSEntry{
			Kid:          prev.Kid,
			Alg:          "EdDSA",
			PublicKeyB64: base64.StdEncoding.EncodeToString(prev.PublicKey),
			CreatedAt:    prev.CreatedAt.UTC().Format(time.RFC3339),
			RetiresAt:    retiresAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// JWKSHandler returns an http.Handler that serves the JWKS payload
// at `GET /api/.well-known/server-identity.json` with a 5-minute
// cache header.
func JWKSHandler(s *Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := json.Marshal(s.JWKS())
		if err != nil {
			http.Error(w, "jwks unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(body)
	})
}
