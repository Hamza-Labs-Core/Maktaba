package telemetry

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/client"
)

// ServerReport is the home's report of a day (contract: Maktaba Cloud's
// POST /v1/telemetry, a batch of type "server"). Every count is a range;
// nothing in it names a file, a title, a person or an address.
type ServerReport struct {
	Version     string         `json:"version"`
	OS          string         `json:"os"`
	Arch        string         `json:"arch"`
	InstallKind string         `json:"install_kind"`
	LibrarySize string         `json:"library_size"`
	Libraries   string         `json:"libraries"`
	Users       string         `json:"users"`
	Machines    string         `json:"machines"`
	Apps        []AppUse       `json:"apps"`
	Features    map[string]any `json:"features"`
	Errors      []ErrorCount   `json:"errors"`
}

// AppUse is one app on one platform over the last week, from Epic 44's
// api_usage_daily (requests, errors) and api_usage_people_daily (people).
type AppUse struct {
	App      string `json:"app"`
	Platform string `json:"platform"`
	People   string `json:"people"`
	Requests string `json:"requests"`
	Errors   string `json:"errors"`
}

// appsWindow is how far back the report's app use reaches.
const appsWindow = 7 * 24 * time.Hour

// maxApps bounds the report's (app, platform) rows.
const maxApps = 50

// Reporter builds the home's report from its database.
type Reporter struct {
	DB      *sql.DB
	Version string
	// InstallKind says how the home was installed (nil: DefaultInstallKind).
	InstallKind func() string
	// RemoteAccess reports whether the home is linked to Maktaba Cloud for
	// remote access (nil: not reported).
	RemoteAccess func() bool
	Errors       *ErrorCounts
	Logger       *slog.Logger
}

// DefaultInstallKind is how this home was installed, as the report says it:
// "docker" in a container, else the Mac app on macOS (the only way Maktaba
// ships there), "windows" or "linux".
func DefaultInstallKind() string {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "docker"
	}
	switch runtime.GOOS {
	case "darwin":
		return "mac-app"
	case "windows", "linux":
		return runtime.GOOS
	}
	return "other"
}

func (r *Reporter) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

// count is a count query's result as a range; "" when the query failed (a
// table this build lacks), which leaves the field out rather than lying.
func (r *Reporter) count(ctx context.Context, q string, args ...any) string {
	var n int64
	if err := r.DB.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		r.logger().Debug("telemetry: a report count failed", "err", err, "event", "telemetry_report_count")
		return ""
	}
	return Range(n)
}

// Build is the report as of now. peek true leaves the error counts in place
// (the admin's preview); false takes them (the day's report).
func (r *Reporter) Build(ctx context.Context, now time.Time, peek bool) ServerReport {
	kind := DefaultInstallKind
	if r.InstallKind != nil {
		kind = r.InstallKind
	}
	rep := ServerReport{
		Version:     r.Version,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		InstallKind: kind(),
		LibrarySize: r.count(ctx, `SELECT count(*) FROM videos`),
		Libraries:   r.count(ctx, `SELECT count(*) FROM libraries`),
		Users:       r.count(ctx, `SELECT count(*) FROM users`),
		Machines:    r.count(ctx, `SELECT count(*) FROM cluster_nodes`),
		Apps:        r.apps(ctx, now),
		Features:    r.features(ctx),
		Errors:      []ErrorCount{},
	}
	if rep.Machines == "" || rep.Machines == "0" {
		rep.Machines = "1" // a home with no machines added is one machine
	}
	if r.Errors != nil {
		errs := r.Errors.Take()
		if peek {
			r.Errors.Put(errs)
		}
		rep.Errors = scrubErrors(errs)
	}
	return rep
}

// scrubErrors keeps the kinds that are event names (anything else is counted
// as "other"), the most frequent maxReportErrors.
func scrubErrors(in []ErrorCount) []ErrorCount {
	merged := map[string]int{}
	var order []string
	for _, e := range in {
		if e.Count <= 0 {
			continue
		}
		m := e.Message
		if !ErrorKindRe.MatchString(m) {
			m = OtherError
		}
		if _, ok := merged[m]; !ok {
			order = append(order, m)
		}
		merged[m] += e.Count
	}
	out := make([]ErrorCount, 0, len(order))
	for _, m := range order {
		out = append(out, ErrorCount{Message: m, Count: merged[m]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	if len(out) > maxReportErrors {
		out = out[:maxReportErrors]
	}
	return out
}

// apps is each (app, platform)'s use over the last week, the busiest first.
// People are counted per app (api_usage_people_daily has no platform): each of
// an app's platforms says how many people used the app.
func (r *Reporter) apps(ctx context.Context, now time.Time) []AppUse {
	since := now.Add(-appsWindow).UTC().Format("2006-01-02")
	people := map[string]int64{}
	if rows, err := r.DB.QueryContext(ctx, `SELECT client_app, count(DISTINCT user_id)
		FROM api_usage_people_daily WHERE day >= $1 GROUP BY client_app`, since); err == nil {
		for rows.Next() {
			var app string
			var n int64
			if rows.Scan(&app, &n) == nil {
				people[app] = n
			}
		}
		_ = rows.Close()
	}
	rows, err := r.DB.QueryContext(ctx, `SELECT client_app, platform, sum(requests), sum(errors)
		FROM api_usage_daily WHERE day >= $1 GROUP BY client_app, platform
		ORDER BY sum(requests) DESC, client_app, platform LIMIT $2`, since, maxApps)
	if err != nil {
		r.logger().Debug("telemetry: the report's app use failed", "err", err, "event", "telemetry_report_apps")
		return []AppUse{}
	}
	defer rows.Close()
	out := []AppUse{}
	for rows.Next() {
		var app, platform string
		var requests, errs int64
		if err := rows.Scan(&app, &platform, &requests, &errs); err != nil {
			continue
		}
		info := client.Parse(app + "/x (" + platform + ")") // bounded, lowercase tokens
		out = append(out, AppUse{App: info.App, Platform: info.Platform,
			People: Range(people[app]), Requests: Range(requests), Errors: Range(errs)})
	}
	return out
}

// FeatureCounts are the report's feature counts (ranges) and what each
// counts: rows of the home's own tables, never what they hold.
var FeatureCounts = map[string]string{
	"channels":         `SELECT count(*) FROM channels`,
	"collections":      `SELECT count(*) FROM collections`,
	"transcripts":      `SELECT count(*) FROM transcripts`,
	"subtitle_files":   `SELECT count(*) FROM subtitle_files`,
	"access_tokens":    `SELECT count(*) FROM personal_access_tokens`,
	"paired_devices":   `SELECT count(*) FROM devices`,
	"library_acl_rows": `SELECT count(*) FROM library_acl`,
	"extensions":       `SELECT count(*) FROM extensions`,
}

// FeatureFlags are the report's yes/no features.
var FeatureFlags = []string{"open_registration", "remote_access"}

// features are which parts of Maktaba the home uses: FeatureCounts as ranges
// and FeatureFlags as booleans, nothing else (Maktaba Cloud refuses any other
// name).
func (r *Reporter) features(ctx context.Context) map[string]any {
	f := map[string]any{}
	for name, q := range FeatureCounts {
		if v := r.count(ctx, q); v != "" {
			f[name] = v
		}
	}
	var open string
	if err := r.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'auth.open_registration'`).Scan(&open); err == nil {
		f["open_registration"] = strings.Contains(strings.ToLower(open), "true")
	} else {
		f["open_registration"] = false
	}
	if r.RemoteAccess != nil {
		f["remote_access"] = r.RemoteAccess()
	}
	return f
}
