package machineagent

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteToken writes a fresh per-boot token (0600) and returns it (design §9.2:
// <data_dir>/supervisor/local.token, read by the node's API).
func WriteToken(path string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tok, nil
}

// Handler serves the agent's loopback endpoints, behind the token:
//
//	GET /agent/state   connection, key id, last error
//	GET /agent/keys    the delegation key (JSON {delegation:{id,key,not_after}})
//	GET /agent/events  text/event-stream: "keys" (the current one first) and "revoked"
func (a *Agent) Handler(token string) http.Handler {
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/agent/state", auth(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a.Status())
	}))
	mux.HandleFunc("/agent/keys", auth(func(w http.ResponseWriter, _ *http.Request) {
		k := a.CurrentKey()
		if k == nil {
			http.Error(w, "no delegation key yet", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"delegation": k})
	}))
	mux.HandleFunc("/agent/events", auth(func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		fl.Flush()
		ch, cancel := a.Subscribe()
		defer cancel()
		keep := time.NewTicker(15 * time.Second)
		defer keep.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-keep.C:
				_, _ = fmt.Fprint(w, ": keep-alive\n\n")
				fl.Flush()
			case e, ok := <-ch:
				if !ok {
					return
				}
				var data []byte
				switch e.Kind {
				case "keys":
					data, _ = json.Marshal(map[string]any{"delegation": e.Keys})
				case "revoked":
					data, _ = json.Marshal(e.Revoked)
				default:
					continue
				}
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Kind, data)
				fl.Flush()
			}
		}
	}))
	return mux
}
