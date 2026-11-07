package admin

// The cloud's own logs and errors for the service admin (Epic 53 story 53.17 §2–3). The index
// runs on the witness W over every node's lines; the api role asks it over the private
// network with W's token:
//
//	GET /v1/admin/logs/search    the cloud's lines (?q, service, level, node, request_id, since, until, cursor, limit)
//	GET /v1/admin/logs/around    the lines around one (?ts, service, node)
//	GET /v1/admin/errors         the cloud's own error groups beside the homes' anonymous ones,
//	                             with the problem reports that name each (53.16)
//	GET /v1/admin/errors/cloud/{fp}  one of the cloud's groups
//
// Before W runs (Epic 50.07) they answer 503 logs_unavailable: each node's own copy is all
// there is, read on the node.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// LogsSource is where the cloud's log index answers (W's private address and token).
type LogsSource struct {
	URL, Token string
	Client     *http.Client
}

func (s *LogsSource) ok() bool { return s != nil && s.URL != "" && s.Token != "" }

// get asks W's index for path (?query) and answers its status and body.
func (s *LogsSource) get(ctx context.Context, path string, q url.Values) (int, []byte, error) {
	u := strings.TrimRight(s.URL, "/") + "/v1/logs" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	cl := s.Client
	if cl == nil {
		cl = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, b, err
}

// the query keys passed on to the index
var logQueryKeys = []string{"q", "service", "component", "level", "node", "request_id", "since", "until", "cursor",
	"limit", "ts", "status", "sort"}

func passQuery(r *http.Request) url.Values {
	in, out := r.URL.Query(), url.Values{}
	for _, k := range logQueryKeys {
		if v := in.Get(k); v != "" {
			out.Set(k, v)
		}
	}
	return out
}

// proxyLogs answers path from W's index, as it answered.
func (d *Deps) proxyLogs(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !d.Logs.ok() {
			writeErr(w, http.StatusServiceUnavailable, "logs_unavailable", "the cloud's log index (W) is not set up")
			return
		}
		p := path
		if fp := chi.URLParam(r, "fp"); fp != "" {
			p = strings.ReplaceAll(p, "{fp}", url.PathEscape(fp))
		}
		code, body, err := d.Logs.get(r.Context(), p, passQuery(r))
		if err != nil {
			writeErr(w, http.StatusBadGateway, "logs_unreachable", "the cloud's log index (W) did not answer")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(body)
	}
}

// HomeGroup is one of the homes' anonymous error groups (Epic 16.7), with its reports.
type HomeGroup struct {
	Fingerprint string    `json:"fp"`
	Source      string    `json:"source"`
	App         string    `json:"app"`
	Message     string    `json:"message"`
	Count       int64     `json:"count"`
	Installs    int64     `json:"installs"`
	LastVersion string    `json:"last_version"`
	LastSeen    time.Time `json:"last_seen"`
	Reports     int64     `json:"reports"`
}

// Errors is the cloud's own groups (from W) beside the homes' anonymous groups, each with
// the problem reports that name it (?days, default 14).
func (d *Deps) Errors(w http.ResponseWriter, r *http.Request) {
	days := 14
	if v := r.URL.Query().Get("days"); v == "1" || v == "7" || v == "30" || v == "90" {
		days = map[string]int{"1": 1, "7": 7, "30": 30, "90": 90}[v]
	}
	out := map[string]any{"days": days}
	// the cloud's own groups: W's index, when it runs
	if d.Logs.ok() {
		code, body, err := d.Logs.get(r.Context(), "/errors", passQuery(r))
		var groups struct {
			Groups []map[string]any `json:"groups"`
		}
		if err == nil && code == http.StatusOK && json.Unmarshal(body, &groups) == nil {
			out["cloud"] = d.withReports(r.Context(), groups.Groups)
		} else {
			out["cloud_error"] = "logs_unreachable"
		}
	} else {
		out["cloud_error"] = "logs_unavailable"
	}
	homes, err := d.homeGroups(r.Context(), d.now().AddDate(0, 0, -days))
	if err != nil {
		d.fail(w, r, "home error groups", err)
		return
	}
	out["homes"] = homes
	writeJSON(w, http.StatusOK, out)
}

// homeGroups is the anonymous groups seen since, the most frequent first.
func (d *Deps) homeGroups(ctx context.Context, since time.Time) ([]HomeGroup, error) {
	rows, err := d.DB.QueryContext(ctx, `
        SELECT g.fingerprint, g.source, g.app, g.message, sum(o.count), count(DISTINCT o.install_id),
               max(o.version), max(o.last_seen),
               (SELECT count(*) FROM problem_reports p WHERE g.fingerprint = ANY(p.fingerprints))
        FROM telemetry_error_groups g JOIN telemetry_error_occurrences o ON o.fingerprint = g.fingerprint
        WHERE o.day >= $1::date
        GROUP BY g.fingerprint, g.source, g.app, g.message
        ORDER BY sum(o.count) DESC LIMIT 200`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HomeGroup{}
	for rows.Next() {
		var g HomeGroup
		if err := rows.Scan(&g.Fingerprint, &g.Source, &g.App, &g.Message, &g.Count, &g.Installs, &g.LastVersion,
			&g.LastSeen, &g.Reports); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// withReports adds each of the cloud's groups' report count (a report from a home can name
// a cloud error's fingerprint when the home's lines carry it).
func (d *Deps) withReports(ctx context.Context, groups []map[string]any) []map[string]any {
	for _, g := range groups {
		fp, _ := g["fp"].(string)
		if fp == "" {
			continue
		}
		var n int64
		_ = d.DB.QueryRowContext(ctx, `SELECT count(*) FROM problem_reports WHERE $1 = ANY(fingerprints)`, fp).Scan(&n)
		g["reports"] = n
	}
	if groups == nil {
		return []map[string]any{}
	}
	return groups
}
