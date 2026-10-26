// Package telemetry is Maktaba Cloud's anonymous usage-data service
// (Epic 16 story 16.7, docs/usage-data.md): the ingest homes post to
// (POST /v1/telemetry), its validation and scrubbing, the store, the
// retention sweep and the operator's aggregate view.
//
// The data is anonymous. Install ids are random, made by the home or
// the app at opt-in, and nothing links them to a cloud account, server,
// user or address. The client address is read only to rate-limit and is
// never stored or logged.
package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Limits of one envelope (the contract).
const (
	MaxBody        = 512 << 10
	MaxBatches     = 20
	MaxEvents      = 100
	MaxVitals      = 50
	MaxReportApps  = 50
	MaxFeatures    = 50
	MaxErrors      = 50
	MaxMessageLen  = 300
	MaxFrameLen    = 200
	MaxStackLen    = 2000
	MaxErrorCount  = 1_000_000_000
	MaxFeatureUses = 1_000_000
	// Event times are clamped into [now-MaxEventAge, now]: a device clock
	// can be wrong and an offline queue can be old.
	MaxEventAge = 30 * 24 * time.Hour
	// A server report's day may be up to a week old (a home that was off)
	// and at most one day ahead (time zones, clock skew).
	MaxReportAge = 7
)

// Ranges is the fixed set every count is reported as.
var Ranges = []string{"0", "1", "2-5", "6-20", "21-100", "101-1k", "1k-10k", "10k-100k", "100k+"}

var rangeSet = func() map[string]bool {
	m := map[string]bool{}
	for _, r := range Ranges {
		m[r] = true
	}
	return m
}()

var (
	reInstallID = regexp.MustCompile(`^[A-Za-z0-9]{16,32}$`)
	reToken     = regexp.MustCompile(`^[a-z0-9._-]{1,32}$`)
	reVersion   = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,39}$`)
	reLocale    = regexp.MustCompile(`^[A-Za-z]{2,3}(?:[_-][A-Za-z0-9]{1,8}){0,3}$`)
	reFeature   = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
	reShortCode = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
	reRoute     = regexp.MustCompile(`^/[A-Za-z0-9/:_.~-]{0,119}$`)
)

// Envelope is the request body.
type Envelope struct {
	Schema  int               `json:"schema"`
	Batches []json.RawMessage `json:"batches"`
}

// Batch is one validated batch: exactly one of Server, App or Forget.
type Batch struct {
	Type      string
	InstallID string
	Server    *ServerReport
	App       *AppBatch
}

// ServerReport is a home's daily report.
type ServerReport struct {
	Day         time.Time
	Version     string
	OS          string
	Arch        string
	InstallKind string
	LibrarySize string
	Libraries   string
	Users       string
	Machines    string
	Apps        []ReportApp
	Features    map[string]any
	Errors      []ReportError
}

// ReportApp is one app line of a server report.
type ReportApp struct {
	App      string `json:"app"`
	Platform string `json:"platform"`
	People   string `json:"people"`
	Requests string `json:"requests"`
	Errors   string `json:"errors,omitempty"`
}

// ReportError is one server error line (scrubbed).
type ReportError struct {
	Message string
	Count   int64
}

// AppBatch is one app install's batch as the home forwards it.
type AppBatch struct {
	App        string
	Platform   string
	FormFactor string
	AppVersion string
	OSVersion  string
	Locale     string
	Events     []Event
	Errors     []AppError
	Vitals     []Vital
}

// Event is an allow-listed app event (not error.uncaught).
type Event struct {
	Kind    string
	Payload map[string]any
	TS      time.Time
}

// AppError is an error.uncaught event, scrubbed.
type AppError struct {
	Message    string
	FirstFrame string
	TS         time.Time
}

// Vital is one web-vitals sample.
type Vital struct {
	Metric string
	Value  float64
	Route  string
}

// ValidationError is a 400: the envelope is rejected as a whole.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// ErrTooManyBatches is a 413: the envelope holds more than MaxBatches.
var ErrTooManyBatches = &ValidationError{Msg: fmt.Sprintf("more than %d batches", MaxBatches)}

func bad(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// strict decodes raw into v, refusing a field v does not name: the hard rule
// (nothing about data, addresses or identities) is enforced by allow-list, so
// a field nobody allowed is an error, never a silent drop.
func strict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}

// batchFields are the fields each type of batch may have, and no other.
var batchFields = map[string]map[string]bool{
	"forget": {"type": true, "install_id": true},
	"server": {"type": true, "install_id": true, "day": true, "report": true},
	"app": {"type": true, "install_id": true, "app": true, "platform": true, "form_factor": true,
		"app_version": true, "os_version": true, "locale": true, "events": true, "vitals": true},
}

// Features are the feature names a server report may carry.
var Features = map[string]bool{
	"channels": true, "collections": true, "transcripts": true, "subtitle_files": true,
	"access_tokens": true, "paired_devices": true, "library_acl_rows": true, "extensions": true,
	"open_registration": true, "remote_access": true,
}

var (
	// a server error's kind: an event name of the home's logs
	reServerErrorKind = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	// an app error's kind: its class (TypeError), never its message
	reAppErrorKind = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$.]{0,63}$`)
	// a code frame as the home sends it: "function (file.js:line:col)"
	reCodeFrame = regexp.MustCompile(`^[A-Za-z0-9_$.<>]{1,80} \([A-Za-z0-9_.-]{1,80}\.(?:js|mjs|cjs|ts|tsx|jsx|swift|kt|java|go):\d{1,7}(?::\d{1,7})?\)$`)
)

// Parse validates an envelope body and returns its batches. now is the
// clock used for the day window and the event-time clamp.
func Parse(body []byte, now time.Time) ([]Batch, error) {
	var env Envelope
	if err := strict(body, &env); err != nil {
		return nil, bad("body is not a JSON envelope of schema and batches only")
	}
	if env.Schema != 1 {
		return nil, bad("schema must be 1")
	}
	if len(env.Batches) == 0 {
		return nil, bad("no batches")
	}
	if len(env.Batches) > MaxBatches {
		return nil, ErrTooManyBatches
	}
	out := make([]Batch, 0, len(env.Batches))
	for i, raw := range env.Batches {
		b, err := parseBatch(raw, now)
		if err != nil {
			return nil, bad("batch %d: %s", i, err.Error())
		}
		out = append(out, b)
	}
	return out, nil
}

type rawBatch struct {
	Type      string `json:"type"`
	InstallID string `json:"install_id"`
	// server
	Day    string          `json:"day"`
	Report json.RawMessage `json:"report"`
	// app
	App        string            `json:"app"`
	Platform   string            `json:"platform"`
	FormFactor string            `json:"form_factor"`
	AppVersion string            `json:"app_version"`
	OSVersion  string            `json:"os_version"`
	Locale     string            `json:"locale"`
	Events     []json.RawMessage `json:"events"`
	Vitals     []json.RawMessage `json:"vitals"`
}

func parseBatch(raw json.RawMessage, now time.Time) (Batch, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Batch{}, bad("not a batch object")
	}
	var typ string
	_ = json.Unmarshal(fields["type"], &typ)
	allowed, known := batchFields[typ]
	if !known {
		return Batch{}, bad("unknown batch type %q", typ)
	}
	for k := range fields {
		if !allowed[k] {
			return Batch{}, bad("field %q is not allowed in a %s batch", k, typ)
		}
	}
	var rb rawBatch
	if err := strict(raw, &rb); err != nil {
		return Batch{}, bad("not a batch object")
	}
	if !reInstallID.MatchString(rb.InstallID) {
		return Batch{}, bad("install_id must be 16-32 letters or digits")
	}
	b := Batch{Type: rb.Type, InstallID: rb.InstallID}
	switch rb.Type {
	case "forget":
		return b, nil
	case "server":
		rep, err := parseServer(rb, now)
		if err != nil {
			return Batch{}, err
		}
		b.Server = rep
		return b, nil
	case "app":
		ab, err := parseApp(rb, now)
		if err != nil {
			return Batch{}, err
		}
		b.App = ab
		return b, nil
	default:
		return Batch{}, bad("unknown batch type %q", rb.Type)
	}
}

type rawReport struct {
	Version     string                     `json:"version"`
	OS          string                     `json:"os"`
	Arch        string                     `json:"arch"`
	InstallKind string                     `json:"install_kind"`
	LibrarySize string                     `json:"library_size"`
	Libraries   string                     `json:"libraries"`
	Users       string                     `json:"users"`
	Machines    string                     `json:"machines"`
	Apps        []ReportApp                `json:"apps"`
	Features    map[string]json.RawMessage `json:"features"`
	Errors      []struct {
		Message string `json:"message"`
		Count   int64  `json:"count"`
	} `json:"errors"`
}

func parseServer(rb rawBatch, now time.Time) (*ServerReport, error) {
	day, err := time.Parse("2006-01-02", rb.Day)
	if err != nil {
		return nil, bad("day must be YYYY-MM-DD")
	}
	today := now.UTC().Truncate(24 * time.Hour)
	if day.After(today.AddDate(0, 0, 1)) || day.Before(today.AddDate(0, 0, -MaxReportAge)) {
		return nil, bad("day is outside the accepted window")
	}
	if len(rb.Report) == 0 {
		return nil, bad("report missing")
	}
	var rr rawReport
	if err := strict(rb.Report, &rr); err != nil {
		return nil, bad("report is malformed or has a field that is not allowed")
	}
	rep := &ServerReport{Day: day, Version: rr.Version, OS: rr.OS, Arch: rr.Arch, InstallKind: rr.InstallKind,
		LibrarySize: rr.LibrarySize, Libraries: rr.Libraries, Users: rr.Users, Machines: rr.Machines,
		Features: map[string]any{}}
	if !reVersion.MatchString(rep.Version) {
		return nil, bad("report.version is invalid")
	}
	for name, v := range map[string]string{"os": rep.OS, "arch": rep.Arch, "install_kind": rep.InstallKind} {
		if !reToken.MatchString(v) {
			return nil, bad("report.%s is invalid", name)
		}
	}
	for name, v := range map[string]string{"library_size": rep.LibrarySize, "libraries": rep.Libraries,
		"users": rep.Users, "machines": rep.Machines} {
		if !rangeSet[v] {
			return nil, bad("report.%s is not a range", name)
		}
	}
	if len(rr.Apps) > MaxReportApps {
		return nil, bad("report.apps has more than %d entries", MaxReportApps)
	}
	for i, a := range rr.Apps {
		if !reToken.MatchString(a.App) || !reToken.MatchString(a.Platform) {
			return nil, bad("report.apps[%d] app or platform is invalid", i)
		}
		if !rangeSet[a.People] || !rangeSet[a.Requests] || (a.Errors != "" && !rangeSet[a.Errors]) {
			return nil, bad("report.apps[%d] has a count that is not a range", i)
		}
	}
	rep.Apps = rr.Apps
	if rep.Apps == nil {
		rep.Apps = []ReportApp{}
	}
	if len(rr.Features) > MaxFeatures {
		return nil, bad("report.features has more than %d keys", MaxFeatures)
	}
	for name, raw := range rr.Features {
		if !Features[name] {
			return nil, bad("report.features key %q is not allowed", name)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, bad("report.features.%s is malformed", name)
		}
		switch x := v.(type) {
		case bool:
			rep.Features[name] = x
		case string:
			if !rangeSet[x] {
				return nil, bad("report.features.%s is not a bool or a range", name)
			}
			rep.Features[name] = x
		default:
			return nil, bad("report.features.%s is not a bool or a range", name)
		}
	}
	if len(rr.Errors) > MaxErrors {
		return nil, bad("report.errors has more than %d entries", MaxErrors)
	}
	for i, e := range rr.Errors {
		if e.Count < 0 || e.Count > MaxErrorCount {
			return nil, bad("report.errors[%d].count is out of range", i)
		}
		msg := e.Message
		if !reServerErrorKind.MatchString(msg) {
			return nil, bad("report.errors[%d].message is not an error kind", i)
		}
		rep.Errors = append(rep.Errors, ReportError{Message: msg, Count: e.Count})
	}
	return rep, nil
}

type rawEvent struct {
	Kind    string                     `json:"kind"`
	Payload map[string]json.RawMessage `json:"payload"`
	TS      string                     `json:"ts"`
}

type rawVital struct {
	Metric string   `json:"metric"`
	Value  *float64 `json:"value"`
	Route  string   `json:"route"`
}

// VitalMetrics are the accepted web-vitals names.
var VitalMetrics = map[string]bool{"LCP": true, "INP": true, "CLS": true, "FCP": true, "TTFB": true, "FID": true}

func parseApp(rb rawBatch, now time.Time) (*AppBatch, error) {
	ab := &AppBatch{App: rb.App, Platform: rb.Platform, FormFactor: rb.FormFactor,
		AppVersion: rb.AppVersion, OSVersion: rb.OSVersion, Locale: rb.Locale}
	if !reToken.MatchString(ab.App) {
		return nil, bad("app is invalid")
	}
	if !reToken.MatchString(ab.Platform) {
		return nil, bad("platform is invalid")
	}
	if ab.FormFactor != "" && !reToken.MatchString(ab.FormFactor) {
		return nil, bad("form_factor is invalid")
	}
	if !reVersion.MatchString(ab.AppVersion) {
		return nil, bad("app_version is invalid")
	}
	if ab.OSVersion != "" && !reVersion.MatchString(ab.OSVersion) {
		return nil, bad("os_version is invalid")
	}
	if ab.Locale != "" && (len(ab.Locale) > 20 || !reLocale.MatchString(ab.Locale)) {
		return nil, bad("locale is invalid")
	}
	if len(rb.Events) > MaxEvents {
		return nil, bad("more than %d events", MaxEvents)
	}
	if len(rb.Vitals) > MaxVitals {
		return nil, bad("more than %d vitals", MaxVitals)
	}
	for i, raw := range rb.Events {
		var re rawEvent
		if err := strict(raw, &re); err != nil {
			return nil, bad("events[%d] is malformed or has a field that is not allowed", i)
		}
		ts, err := time.Parse(time.RFC3339Nano, re.TS)
		if err != nil {
			return nil, bad("events[%d].ts is not RFC3339", i)
		}
		ts = clampTS(ts, now)
		payload, err := parsePayload(re.Kind, re.Payload)
		if err != nil {
			return nil, bad("events[%d]: %s", i, err.Error())
		}
		if re.Kind == "error.uncaught" {
			msg, _ := payload["error_message"].(string)
			stack, _ := payload["stack_first_5"].(string)
			if !reAppErrorKind.MatchString(msg) {
				return nil, bad("events[%d].payload.error_message is not an error kind", i)
			}
			frames := strings.Split(stack, "\n")
			for _, f := range frames {
				if stack != "" && !reCodeFrame.MatchString(f) {
					return nil, bad("events[%d].payload.stack_first_5 has a line that is not a code frame", i)
				}
			}
			ab.Errors = append(ab.Errors, AppError{Message: msg, FirstFrame: frames[0], TS: ts})
			continue
		}
		ab.Events = append(ab.Events, Event{Kind: re.Kind, Payload: payload, TS: ts})
	}
	for i, raw := range rb.Vitals {
		var rv rawVital
		if err := strict(raw, &rv); err != nil {
			return nil, bad("vitals[%d] is malformed or has a field that is not allowed", i)
		}
		if !VitalMetrics[rv.Metric] {
			return nil, bad("vitals[%d].metric is unknown", i)
		}
		if rv.Value == nil || math.IsNaN(*rv.Value) || math.IsInf(*rv.Value, 0) || *rv.Value < 0 || *rv.Value > 600_000 {
			return nil, bad("vitals[%d].value is out of range", i)
		}
		route := rv.Route
		if j := strings.IndexAny(route, "?#"); j >= 0 {
			route = route[:j]
		}
		if route != "" && !reRoute.MatchString(route) {
			return nil, bad("vitals[%d].route is invalid", i)
		}
		ab.Vitals = append(ab.Vitals, Vital{Metric: rv.Metric, Value: *rv.Value, Route: route})
	}
	return ab, nil
}

// field kinds of the event allow-list.
type fieldKind int

const (
	fBool fieldKind = iota
	fCount
	fDuration
	fFeature
	fSource
	fCode
	fText
	fStack
)

// EventKinds is the allow-list: kind → field → value kind. Fields not
// listed are dropped.
var EventKinds = map[string]map[string]fieldKind{
	"app.open":       {"cold_start": fBool},
	"feature.used":   {"feature": fFeature, "count": fCount},
	"player.start":   {"source": fSource, "codec": fCode},
	"player.error":   {"error_kind": fCode, "duration_ms": fDuration},
	"error.uncaught": {"error_message": fText, "stack_first_5": fStack},
}

var playerSources = map[string]bool{"direct": true, "transcode": true, "live": true}

func parsePayload(kind string, raw map[string]json.RawMessage) (map[string]any, error) {
	fields, ok := EventKinds[kind]
	if !ok {
		return nil, bad("unknown event kind %q", kind)
	}
	out := map[string]any{}
	// deterministic error messages
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := bytes.TrimSpace(raw[k])
		if len(v) > 0 && (v[0] == '{' || v[0] == '[') {
			return nil, bad("payload.%s is not a scalar", k)
		}
		fk, known := fields[k]
		if !known {
			return nil, bad("payload.%s is not allowed", k)
		}
		var x any
		if err := json.Unmarshal(v, &x); err != nil {
			return nil, bad("payload.%s is malformed", k)
		}
		if x == nil {
			continue
		}
		val, err := checkField(fk, x)
		if err != nil {
			return nil, bad("payload.%s %s", k, err.Error())
		}
		out[k] = val
	}
	return out, nil
}

func checkField(fk fieldKind, x any) (any, error) {
	switch fk {
	case fBool:
		b, ok := x.(bool)
		if !ok {
			return nil, bad("must be a bool")
		}
		return b, nil
	case fCount, fDuration:
		f, ok := x.(float64)
		limit := float64(MaxFeatureUses)
		if fk == fDuration {
			limit = 7 * 24 * 3600 * 1000
		}
		if !ok || f != math.Trunc(f) || f < 0 || f > limit {
			return nil, bad("must be a whole number in range")
		}
		return int64(f), nil
	case fFeature:
		s, ok := x.(string)
		if !ok || !reFeature.MatchString(s) {
			return nil, bad("must be a short area name")
		}
		return s, nil
	case fSource:
		s, ok := x.(string)
		if !ok || !playerSources[s] {
			return nil, bad("must be direct, transcode or live")
		}
		return s, nil
	case fCode:
		s, ok := x.(string)
		if !ok || !reShortCode.MatchString(s) {
			return nil, bad("must be a short code")
		}
		return s, nil
	case fText, fStack:
		s, ok := x.(string)
		if !ok {
			return nil, bad("must be a string")
		}
		if fk == fStack {
			return capRunes(s, MaxStackLen), nil
		}
		return s, nil
	}
	return nil, bad("unknown field")
}

func clampTS(ts, now time.Time) time.Time {
	if ts.After(now) {
		return now
	}
	if oldest := now.Add(-MaxEventAge); ts.Before(oldest) {
		return oldest
	}
	return ts
}

func capRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
