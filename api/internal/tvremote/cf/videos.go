package cf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Video is a Stream video (a live broadcast or its recording).
type Video struct {
	UID       string `json:"uid"`
	LiveInput string `json:"liveInput,omitempty"`
	Status    struct {
		State string `json:"state"` // live-inprogress | ready | ...
	} `json:"status"`
	Playback struct {
		HLS  string `json:"hls"`
		DASH string `json:"dash"`
	} `json:"playback"`
	Duration float64 `json:"duration"`
	Created  string  `json:"created,omitempty"`
}

// ListVideos returns up to limit videos of the account (newest first).
func (c *Client) ListVideos(ctx context.Context, limit int) ([]Video, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", fmt.Sprint(limit))
	}
	path := c.account() + "/stream"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out []Video
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// InputVideos returns the videos of a live input, the in-progress
// broadcast first when there is one.
func (c *Client) InputVideos(ctx context.Context, inputUID string) ([]Video, error) {
	var out []Video
	err := c.do(ctx, http.MethodGet, c.account()+"/stream/live_inputs/"+url.PathEscape(inputUID)+"/videos", nil, &out)
	return out, err
}

// DeleteVideo deletes a video (a recording frees its storage).
func (c *Client) DeleteVideo(ctx context.Context, uid string) error {
	return c.do(ctx, http.MethodDelete, c.account()+"/stream/"+url.PathEscape(uid), nil, nil)
}

// StorageUsage is GET /accounts/{id}/stream/storage-usage.
type StorageUsage struct {
	TotalStorageMinutes      float64 `json:"totalStorageMinutes"`
	TotalStorageMinutesLimit float64 `json:"totalStorageMinutesLimit"`
	VideoCount               int     `json:"videoCount"`
}

// StorageUsage returns the minutes stored and the account's capacity.
func (c *Client) StorageUsage(ctx context.Context) (StorageUsage, error) {
	var u StorageUsage
	err := c.do(ctx, http.MethodGet, c.account()+"/stream/storage-usage", nil, &u)
	return u, err
}

var customerRe = regexp.MustCompile(`customer-([a-z0-9]+)\.cloudflarestream\.com`)

// CustomerCode finds the customer subdomain code in any text (a playback
// URL, an API answer); "" when there is none.
func CustomerCode(s string) string {
	m := customerRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

// CustomerBaseURL is https://customer-<code>.cloudflarestream.com (or the
// test override).
func (c *Client) CustomerBaseURL(code string) string {
	if c.CustomerBase != "" {
		return strings.TrimRight(c.CustomerBase, "/")
	}
	return "https://customer-" + code + ".cloudflarestream.com"
}

// Lifecycle is GET customer-<code>.cloudflarestream.com/{input}/lifecycle.
type Lifecycle struct {
	IsInput  bool   `json:"isInput"`
	VideoUID string `json:"videoUID"`
	Live     bool   `json:"live"`
}

// Lifecycle says whether an input is live and its in-progress video id.
func (c *Client) Lifecycle(ctx context.Context, code, inputUID string) (Lifecycle, error) {
	var lc Lifecycle
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.CustomerBaseURL(code)+"/"+url.PathEscape(inputUID)+"/lifecycle", nil)
	if err != nil {
		return lc, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return lc, fmt.Errorf("cloudflare lifecycle: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return lc, &APIError{Status: resp.StatusCode, Method: http.MethodGet, Path: "/" + inputUID + "/lifecycle"}
	}
	err = json.Unmarshal(b, &lc)
	return lc, err
}

// ManifestURL is the HLS manifest of a video or input id, or of a signed
// token in its place (design §7.6).
func (c *Client) ManifestURL(code, idOrToken string) string {
	return c.CustomerBaseURL(code) + "/" + idOrToken + "/manifest/video.m3u8"
}
