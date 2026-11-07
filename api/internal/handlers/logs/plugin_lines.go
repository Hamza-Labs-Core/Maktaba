package logs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Limits of a plugin's lines sent with a store report (story 54.18, #758).
const (
	PluginLinesMax   = 200
	PluginLinesBytes = 256 << 10
	PluginLinesSince = 24 * time.Hour
)

// IndexAddr is the logs index's address (MAKTABA_LOGS_ADDR; "" when none runs).
func IndexAddr() string { return strings.TrimSpace(os.Getenv(EnvLogsAddr)) }

// PluginLines are a plugin's own recent lines (service plugin.<id>, the last 24 hours, the
// newest PluginLinesMax and at most PluginLinesBytes), each scrubbed by the shared redaction
// rules as a problem report's are: what an admin attaches to a report of the plugin to its
// store (#758). Nothing when the index is not running or does not answer.
func PluginLines(ctx context.Context, base, pluginID string, now time.Time) []json.RawMessage {
	if base == "" || pluginID == "" {
		return nil
	}
	q := url.Values{
		"service": {"plugin." + pluginID},
		"since":   {now.Add(-PluginLinesSince).UTC().Format(time.RFC3339)},
		"limit":   {"200"},
	}
	u := url.URL{Scheme: "http", Host: base, Path: "/search", RawQuery: q.Encode()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var res struct {
		Lines []json.RawMessage `json:"lines"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&res) != nil {
		return nil
	}
	out := []json.RawMessage{}
	size := 0
	for _, l := range res.Lines {
		var head struct {
			Service string `json:"service"`
		}
		// only the plugin's own lines, whatever the index answered
		if json.Unmarshal(l, &head) != nil || head.Service != "plugin."+pluginID {
			continue
		}
		s := scrubRaw(l)
		if len(out) >= PluginLinesMax || size+len(s) > PluginLinesBytes {
			break
		}
		out = append(out, s)
		size += len(s)
	}
	return out
}
