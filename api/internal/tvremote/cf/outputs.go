package cf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// Output is a live input's output (simulcast destination, 32.13). Its
// stream key is a secret.
type Output struct {
	UID       string `json:"uid"`
	URL       string `json:"url"`
	StreamKey string `json:"streamKey,omitempty"`
	Enabled   *bool  `json:"enabled,omitempty"`
	// Raw is the whole answer: whether Cloudflare reports the
	// destination's state is one of the spike's questions (32.6 AC7g,
	// README Q13).
	Raw json.RawMessage `json:"-"`
}

// Status is a per-output connection status when Cloudflare's answer has
// one ("status" or "state": a string, or {"current":{"state","reason"}});
// "" when the documented object (uid, url, streamKey, enabled) is all
// there is. Whether it ever has one is README Q13.
func (o Output) Status() string {
	var m map[string]json.RawMessage
	if len(o.Raw) == 0 || json.Unmarshal(o.Raw, &m) != nil {
		return ""
	}
	for _, k := range []string{"status", "state"} {
		v, ok := m[k]
		if !ok || string(v) == "null" {
			continue
		}
		if st := ParseInputStatus(v); st.State != "" {
			if st.Reason != "" {
				return st.State + ": " + st.Reason
			}
			return st.State
		}
	}
	return ""
}

func (c *Client) outputsPath(inputUID string) string {
	return c.account() + "/stream/live_inputs/" + url.PathEscape(inputUID) + "/outputs"
}

func decodeOutput(raw json.RawMessage) (Output, error) {
	var o Output
	err := json.Unmarshal(raw, &o)
	o.Raw = raw
	return o, err
}

// CreateOutput adds an output to a live input: Cloudflare retransmits what
// the input receives to url + streamKey (POST …/live_inputs/{input}/outputs).
func (c *Client) CreateOutput(ctx context.Context, inputUID, outURL, streamKey string, enabled bool) (Output, error) {
	var raw json.RawMessage
	err := c.do(ctx, http.MethodPost, c.outputsPath(inputUID),
		map[string]any{"url": outURL, "streamKey": streamKey, "enabled": enabled}, &raw)
	if err != nil {
		return Output{}, err
	}
	return decodeOutput(raw)
}

// UpdateOutput enables or disables an output (PUT …/outputs/{output}).
func (c *Client) UpdateOutput(ctx context.Context, inputUID, outputUID string, enabled bool) (Output, error) {
	var raw json.RawMessage
	err := c.do(ctx, http.MethodPut, c.outputsPath(inputUID)+"/"+url.PathEscape(outputUID),
		map[string]any{"enabled": enabled}, &raw)
	if err != nil {
		return Output{}, err
	}
	return decodeOutput(raw)
}

// DeleteOutput deletes an output; Cloudflare disconnects it within 30 s.
func (c *Client) DeleteOutput(ctx context.Context, inputUID, outputUID string) error {
	return c.do(ctx, http.MethodDelete, c.outputsPath(inputUID)+"/"+url.PathEscape(outputUID), nil, nil)
}

// ListOutputs lists a live input's outputs, each with its raw answer.
func (c *Client) ListOutputs(ctx context.Context, inputUID string) ([]Output, error) {
	var raws []json.RawMessage
	if err := c.do(ctx, http.MethodGet, c.outputsPath(inputUID), nil, &raws); err != nil {
		return nil, err
	}
	out := make([]Output, 0, len(raws))
	for _, r := range raws {
		o, err := decodeOutput(r)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}
