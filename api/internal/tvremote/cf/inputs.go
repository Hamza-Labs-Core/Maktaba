package cf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// LiveInputPrefix starts the name (meta.name) of every live input Maktaba
// creates, so removing the integration deletes only Maktaba's inputs.
const LiveInputPrefix = "maktaba-"

// Ingest is a live input's RTMPS or SRT endpoint. The stream key and
// passphrase are secrets: callers keep them in home_secrets.
type Ingest struct {
	URL        string `json:"url"`
	StreamKey  string `json:"streamKey,omitempty"`
	StreamID   string `json:"streamId,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

// Recording is a live input's recording settings.
type Recording struct {
	Mode              string   `json:"mode,omitempty"` // off | automatic
	RequireSignedURLs bool     `json:"requireSignedURLs"`
	AllowedOrigins    []string `json:"allowedOrigins,omitempty"`
	TimeoutSeconds    int      `json:"timeoutSeconds,omitempty"`
}

// LiveInput is a Stream live input.
type LiveInput struct {
	UID              string          `json:"uid"`
	Meta             map[string]any  `json:"meta,omitempty"`
	Created          string          `json:"created,omitempty"`
	Modified         string          `json:"modified,omitempty"`
	Enabled          *bool           `json:"enabled,omitempty"`
	Recording        *Recording      `json:"recording,omitempty"`
	RTMPS            *Ingest         `json:"rtmps,omitempty"`
	RTMPSPlayback    *Ingest         `json:"rtmpsPlayback,omitempty"`
	SRT              *Ingest         `json:"srt,omitempty"`
	Status           json.RawMessage `json:"status,omitempty"`
	PreferLowLatency bool            `json:"preferLowLatency,omitempty"`
	DeleteAfterDays  *int            `json:"deleteRecordingAfterDays,omitempty"`
}

// Name is the input's meta.name.
func (in LiveInput) Name() string {
	if in.Meta == nil {
		return ""
	}
	s, _ := in.Meta["name"].(string)
	return s
}

// CreateLiveInput is POST /accounts/{id}/stream/live_inputs.
type CreateLiveInput struct {
	Meta             map[string]any `json:"meta,omitempty"`
	Enabled          *bool          `json:"enabled,omitempty"`
	Recording        *Recording     `json:"recording,omitempty"`
	PreferLowLatency bool           `json:"preferLowLatency,omitempty"`
	DeleteAfterDays  *int           `json:"deleteRecordingAfterDays,omitempty"`
}

// ListLiveInputs returns the account's live inputs (the call also proves
// the account has Stream: no subscription answers 403/10xxx).
func (c *Client) ListLiveInputs(ctx context.Context) ([]LiveInput, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, c.account()+"/stream/live_inputs", nil, &raw); err != nil {
		return nil, err
	}
	return decodeInputs(raw)
}

// decodeInputs accepts both shapes Cloudflare has answered with: a plain
// array, or {liveInputs: [...], range, total}.
func decodeInputs(raw json.RawMessage) ([]LiveInput, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var list []LiveInput
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
		return list, nil
	}
	var wrapped struct {
		LiveInputs []LiveInput `json:"liveInputs"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, err
	}
	return wrapped.LiveInputs, nil
}

// GetLiveInput returns one input with its ingest endpoints.
func (c *Client) GetLiveInput(ctx context.Context, uid string) (LiveInput, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, c.account()+"/stream/live_inputs/"+url.PathEscape(uid), nil, &raw); err != nil {
		return LiveInput{}, nil, err
	}
	var in LiveInput
	err := json.Unmarshal(raw, &in)
	return in, raw, err
}

// CreateLiveInput creates an input.
func (c *Client) CreateLiveInput(ctx context.Context, req CreateLiveInput) (LiveInput, error) {
	var in LiveInput
	err := c.do(ctx, http.MethodPost, c.account()+"/stream/live_inputs", req, &in)
	return in, err
}

// SetLiveInputEnabled turns an input on or off (PUT keeps its other settings).
func (c *Client) SetLiveInputEnabled(ctx context.Context, uid string, enabled bool) error {
	return c.do(ctx, http.MethodPut, c.account()+"/stream/live_inputs/"+url.PathEscape(uid),
		map[string]any{"enabled": enabled}, nil)
}

// DeleteLiveInput deletes an input (its recordings stay until deleted).
func (c *Client) DeleteLiveInput(ctx context.Context, uid string) error {
	return c.do(ctx, http.MethodDelete, c.account()+"/stream/live_inputs/"+url.PathEscape(uid), nil, nil)
}

// InputStatus is a live input's status as the API reports it: either a plain
// string or {current: {state, reason, ...}}. Reason may carry an ERR_* code.
type InputStatus struct {
	State  string
	Reason string
}

// ParseInputStatus reads a live input's status field.
func ParseInputStatus(raw json.RawMessage) InputStatus {
	if len(raw) == 0 || string(raw) == "null" {
		return InputStatus{}
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return InputStatus{State: s}
	}
	var obj struct {
		Current *struct {
			State  string `json:"state"`
			Reason string `json:"reason"`
		} `json:"current"`
		State  string `json:"state"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(raw, &obj) != nil {
		return InputStatus{}
	}
	if obj.Current != nil {
		return InputStatus{State: obj.Current.State, Reason: obj.Current.Reason}
	}
	return InputStatus{State: obj.State, Reason: obj.Reason}
}
