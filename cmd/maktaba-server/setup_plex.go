package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// The plex step: "Sign in with Plex" before this install has a database, the
// same plex.tv PIN flow as the Settings page (api handlers/queues
// plexsignin.go). The account token waits in a file of its own in the keys
// folder (0600) until `finish` hands it to `maktaba-api plex-link`, which
// stores the Plex server it reaches like the Settings sign-in does; the
// account token itself is then deleted, never stored.

// plexTVURL is plex.tv (tests point it at a fake).
var plexTVURL = "https://plex.tv"

const (
	plexAuthApp = "https://app.plex.tv/auth"
	plexProduct = "Maktaba"
)

var plexHTTP = &http.Client{Timeout: 15 * time.Second}

// plexTokenPath is the account token's file until finish.
func plexTokenPath() string { return filepath.Join(config.KeysDir(), "plex-setup.token") }

func plexRequest(ctx context.Context, method, path, clientID string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, plexTVURL+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Product", plexProduct)
	req.Header.Set("X-Plex-Client-Identifier", clientID)
	resp, err := plexHTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func newPlexClientID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "maktaba-" + hex.EncodeToString(b), nil
}

// stepPlex: {"action": "skip"} | {"action": "pin"} | {"action": "poll", "pin_id": n}.
func stepPlex(s *setupRun, in json.RawMessage) (any, error) {
	var req struct {
		Action string `json:"action"`
		PinID  int64  `json:"pin_id"`
	}
	if err := decodeInput(in, &req); err != nil {
		return nil, err
	}
	switch req.Action {
	case "skip":
		if err := os.Remove(plexTokenPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := s.updatePending(func(p *setupPending) { p.Plex = nil }); err != nil {
			return nil, err
		}
		return map[string]bool{"linked": false}, nil
	case "pin":
		p, err := s.loadPending()
		if err != nil {
			return nil, err
		}
		clientID := ""
		if p.Plex != nil {
			clientID = p.Plex.ClientID
		}
		if clientID == "" {
			if clientID, err = newPlexClientID(); err != nil {
				return nil, err
			}
		}
		var pin struct {
			ID   int64  `json:"id"`
			Code string `json:"code"`
		}
		code, err := plexRequest(s.ctx, http.MethodPost, "/api/v2/pins?strong=true", clientID, &pin)
		if err != nil || (code != http.StatusCreated && code != http.StatusOK) || pin.ID == 0 || pin.Code == "" {
			return nil, failStep("network", "", "plex.tv did not give a sign-in code (%s).", plexWhy(code, err))
		}
		if err := s.updatePending(func(p *setupPending) {
			p.Plex = &pendingPlex{ClientID: clientID}
		}); err != nil {
			return nil, err
		}
		link := fmt.Sprintf("%s#?clientID=%s&code=%s&context%%5Bdevice%%5D%%5Bproduct%%5D=%s",
			plexAuthApp, url.QueryEscape(clientID), url.QueryEscape(pin.Code), url.QueryEscape(plexProduct))
		return map[string]any{"pin_id": pin.ID, "code": pin.Code, "link_url": link}, nil
	case "poll":
		p, err := s.loadPending()
		if err != nil {
			return nil, err
		}
		if p.Plex == nil || p.Plex.ClientID == "" || req.PinID == 0 {
			return nil, failStep("pin_expired", "", "Start the Plex sign-in again.")
		}
		var pin struct {
			AuthToken *string `json:"authToken"`
		}
		code, err := plexRequest(s.ctx, http.MethodGet, "/api/v2/pins/"+strconv.FormatInt(req.PinID, 10), p.Plex.ClientID, &pin)
		switch {
		case code == http.StatusNotFound:
			return nil, failStep("pin_expired", "", "The Plex code expired.")
		case err != nil || code != http.StatusOK:
			return nil, failStep("network", "", "plex.tv did not answer (%s).", plexWhy(code, err))
		case pin.AuthToken == nil || strings.TrimSpace(*pin.AuthToken) == "":
			return map[string]bool{"linked": false}, nil
		}
		if err := os.MkdirAll(filepath.Dir(plexTokenPath()), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(plexTokenPath(), []byte(strings.TrimSpace(*pin.AuthToken)), 0o600); err != nil {
			return nil, err
		}
		if err := s.updatePending(func(p *setupPending) { p.Plex.Linked = true }); err != nil {
			return nil, err
		}
		return map[string]bool{"linked": true}, nil
	}
	return nil, failStep("bad_input", "", "The plex step takes skip, pin or poll.")
}

func plexWhy(code int, err error) string {
	if err != nil {
		return err.Error()
	}
	return "it answered " + strconv.Itoa(code)
}
