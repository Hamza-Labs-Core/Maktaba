package extcheck

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// DizqueTV runs the dizqueTV extension's Test connection. Read-only:
// GET /api/version, /api/channelNumbers and /api/plex-servers (dizqueTV
// 1.x, src/api.js). dizqueTV has no authentication; nothing secret is
// sent, and of its Plex servers only the names are reported (their
// access tokens are never read into Maktaba's answer).
type DizqueTV struct{ Deps }

type dizqueVersion struct {
	DizqueTV string `json:"dizquetv"`
	FFmpeg   string `json:"ffmpeg"`
	NodeJS   string `json:"nodejs"`
}

// dizquePlexServer is the part of a plex-servers row Maktaba reads: no token.
type dizquePlexServer struct {
	Name string `json:"name"`
	URI  string `json:"uri"`
}

// Test runs the checks: reachable, channels, plex_servers.
func (d *DizqueTV) Test(ctx context.Context) (Result, error) {
	res := Result{CheckedAt: d.now()}
	settings, _, err := d.Ext.ExtensionSettings(ctx, DizqueTVExtension)
	if err != nil {
		return res, err
	}
	add := func(l Line) { res.Lines = append(res.Lines, l) }
	skip := func(keys ...string) {
		for _, k := range keys {
			add(Line{Key: k, Skipped: true, Optional: k == "plex_servers", Detail: "not checked"})
		}
	}
	base, err := baseURL(str(settings, "url"))
	if err != nil {
		add(Line{Key: "reachable", Code: CodeNotConfigured, Detail: err.Error()})
		skip("channels", "plex_servers")
		finish(&res, "dizqueTV is ready")
		return res, nil
	}
	c := d.client()

	a, err := d.get(ctx, c, base, "/api/version", nil)
	reached := false
	switch {
	case err != nil:
		code, det := classify(err, base, d.timeout())
		add(Line{Key: "reachable", Code: code, Detail: det})
	case a.status >= 400:
		add(Line{Key: "reachable", Code: CodeHTTP, LatencyMS: ms(a.latency),
			Detail: fmt.Sprintf("%s answered /api/version with HTTP %d: is this a dizqueTV server?", base.Host, a.status)})
	default:
		var v dizqueVersion
		if derr := decode(a.body, &v); derr != nil || v.DizqueTV == "" {
			add(Line{Key: "reachable", Code: CodeBadAnswer, LatencyMS: ms(a.latency),
				Detail: base.Host + " answered, but not as dizqueTV (no version)"})
		} else {
			reached = true
			det := fmt.Sprintf("%s answered in %d ms (dizqueTV %s", base.Host, a.latency.Milliseconds(), v.DizqueTV)
			if v.FFmpeg != "" {
				det += ", ffmpeg " + v.FFmpeg
			}
			add(Line{Key: "reachable", OK: true, Detail: det + ")", LatencyMS: ms(a.latency)})
		}
	}
	if !reached {
		skip("channels", "plex_servers")
		finish(&res, "dizqueTV is ready")
		d.logResult(res)
		return res, nil
	}

	add(d.channelsLine(ctx, c, base))
	plexURL := ""
	if s, _, perr := d.Ext.ExtensionSettings(ctx, PlexExtension); perr == nil {
		plexURL = str(s, "url")
	}
	add(d.plexServersLine(ctx, c, base, plexURL))
	finish(&res, "dizqueTV is ready")
	d.logResult(res)
	return res, nil
}

func (d *DizqueTV) logResult(res Result) {
	failed := []string{}
	for _, l := range res.Lines {
		if !l.OK && !l.Skipped {
			failed = append(failed, l.Key+"="+l.Code)
		}
	}
	d.log().Info("extensions: dizquetv test connection", "ok", res.OK, "failed", strings.Join(failed, ","),
		"event", "extension_test")
}

func (d *DizqueTV) channelsLine(ctx context.Context, c *http.Client, base *url.URL) Line {
	a, err := d.get(ctx, c, base, "/api/channelNumbers", nil)
	if err != nil {
		code, det := classify(err, base, d.timeout())
		return Line{Key: "channels", Code: code, Detail: det}
	}
	if a.status >= 400 {
		return Line{Key: "channels", Code: CodeHTTP, LatencyMS: ms(a.latency), Detail: fmt.Sprintf("/api/channelNumbers answered HTTP %d", a.status)}
	}
	var nums []any
	if derr := decode(a.body, &nums); derr != nil {
		return Line{Key: "channels", Code: CodeBadAnswer, LatencyMS: ms(a.latency), Detail: "/api/channelNumbers: " + derr.Error()}
	}
	return Line{Key: "channels", OK: true, LatencyMS: ms(a.latency),
		Detail: fmt.Sprintf("%d %s", len(nums), plural(len(nums), "channel", "channels"))}
}

func (d *DizqueTV) plexServersLine(ctx context.Context, c *http.Client, base *url.URL, plexURL string) Line {
	l := Line{Key: "plex_servers", Optional: true}
	a, err := d.get(ctx, c, base, "/api/plex-servers", nil)
	if err != nil {
		l.Code, l.Detail = classify(err, base, d.timeout())
		return l
	}
	l.LatencyMS = ms(a.latency)
	if a.status >= 400 {
		l.Code, l.Detail = CodeHTTP, fmt.Sprintf("/api/plex-servers answered HTTP %d", a.status)
		return l
	}
	var servers []dizquePlexServer
	if derr := decode(a.body, &servers); derr != nil {
		l.Code, l.Detail = CodeBadAnswer, "/api/plex-servers: "+derr.Error()
		return l
	}
	if len(servers) == 0 {
		l.Code, l.Detail = CodeNotFound, "no Plex server is registered in dizqueTV"
		return l
	}
	names := make([]string, 0, len(servers))
	same := ""
	for _, s := range servers {
		n := strings.TrimSpace(s.Name)
		if n == "" {
			n = "(unnamed)"
		}
		names = append(names, n)
		if same == "" && plexURL != "" && SameHost(s.URI, plexURL) {
			same = n
		}
	}
	l.OK = true
	l.Detail = strings.Join(names, ", ")
	switch {
	case plexURL == "":
	case same != "":
		l.Detail += " (" + same + " is Maktaba's Plex)"
	default:
		l.Detail += " (none at Maktaba's Plex address)"
	}
	return l
}
