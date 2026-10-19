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
	// destination's state is one of the spike's questions (32.6 AC7g).
	Raw json.RawMessage `json:"-"`
}

// CreateOutput adds an output to a live input.
func (c *Client) CreateOutput(ctx context.Context, inputUID, outURL, streamKey string) (Output, error) {
	var raw json.RawMessage
	err := c.do(ctx, http.MethodPost, c.account()+"/stream/live_inputs/"+url.PathEscape(inputUID)+"/outputs",
		map[string]any{"url": outURL, "streamKey": streamKey, "enabled": true}, &raw)
	if err != nil {
		return Output{}, err
	}
	var o Output
	err = json.Unmarshal(raw, &o)
	o.Raw = raw
	return o, err
}

// ListOutputs lists a live input's outputs, each with its raw answer.
func (c *Client) ListOutputs(ctx context.Context, inputUID string) ([]Output, error) {
	var raws []json.RawMessage
	if err := c.do(ctx, http.MethodGet, c.account()+"/stream/live_inputs/"+url.PathEscape(inputUID)+"/outputs", nil, &raws); err != nil {
		return nil, err
	}
	out := make([]Output, 0, len(raws))
	for _, r := range raws {
		var o Output
		if err := json.Unmarshal(r, &o); err != nil {
			return nil, err
		}
		o.Raw = r
		out = append(out, o)
	}
	return out, nil
}
