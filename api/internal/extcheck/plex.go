package extcheck

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// PlexExtension is the Plex extension's name (catalogue: queues.extensionCatalog).
const PlexExtension = "plex"

// PlexTokenSecret is where the Plex token is kept (secrets.ExtensionName).
var PlexTokenSecret = secrets.ExtensionName(PlexExtension, "token")

const hdhrUUIDPrefix = "device://tv.plex.grabbers.hdhomerun/"

// Plex runs the Plex extension's Test connection on the home (the token
// lives in home_secrets there).
type Plex struct{ Deps }

type plexContainer[T any] struct {
	MediaContainer T `json:"MediaContainer"`
}

type plexIdentity struct {
	MachineIdentifier string `json:"machineIdentifier"`
	Version           string `json:"version"`
}

type plexRoot struct {
	FriendlyName string `json:"friendlyName"`
	Version      string `json:"version"`
	Platform     string `json:"platform"`
}

type plexSection struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

type plexSections struct {
	Directory []plexSection `json:"Directory"`
}

type plexDevice struct {
	Key      string `json:"key"`
	UUID     string `json:"uuid"`
	URI      string `json:"uri"`
	DeviceID string `json:"deviceId"`
	Title    string `json:"title"`
	Make     string `json:"make"`
	Model    string `json:"model"`
}

type plexDvr struct {
	Key    string                `json:"key"`
	Title  string                `json:"title"`
	Device oneOrMany[plexDevice] `json:"Device"`
}

type plexDvrs struct {
	Dvr oneOrMany[plexDvr] `json:"Dvr"`
}

// oneOrMany: Plex gives one element as an object and several as a list.
type oneOrMany[T any] []T

func (o *oneOrMany[T]) UnmarshalJSON(b []byte) error {
	b = []byte(strings.TrimSpace(string(b)))
	if len(b) > 0 && b[0] == '{' {
		var one T
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*o = []T{one}
		return nil
	}
	var many []T
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*o = many
	return nil
}

// Test runs the checks: reachable, token, server, livetv, maktaba_tuner.
func (p *Plex) Test(ctx context.Context) (Result, error) {
	res := Result{CheckedAt: p.now()}
	settings, _, err := p.Ext.ExtensionSettings(ctx, PlexExtension)
	if err != nil {
		return res, err
	}
	token, tokenCode, tokenProblem := p.token(ctx)
	add := func(l Line) {
		res.Lines = append(res.Lines, Line{
			Key: l.Key, OK: l.OK, Optional: l.Optional, Skipped: l.Skipped,
			Detail: Redact(l.Detail, token), Code: l.Code, LatencyMS: l.LatencyMS,
		})
	}
	skip := func(keys ...string) {
		for _, k := range keys {
			add(Line{Key: k, Skipped: true, Optional: k != "token", Detail: "not checked"})
		}
	}
	after := []string{"server", "livetv", "maktaba_tuner"}

	base, err := baseURL(str(settings, "url"))
	if err != nil {
		add(Line{Key: "reachable", Code: CodeNotConfigured, Detail: err.Error()})
		skip(append([]string{"token"}, after...)...)
		finish(&res, "Plex is ready")
		return res, nil
	}
	c := p.client()

	// reachable: /identity answers without a token
	a, err := p.get(ctx, c, base, "/identity", nil)
	var id plexIdentity
	switch {
	case err != nil:
		code, d := classify(err, base, p.timeout())
		add(Line{Key: "reachable", Code: code, Detail: d})
	case a.status >= 400:
		add(Line{Key: "reachable", Code: CodeHTTP, Detail: fmt.Sprintf("%s answered /identity with HTTP %d: is this a Plex server?", base.Host, a.status), LatencyMS: ms(a.latency)})
	default:
		var doc plexContainer[plexIdentity]
		if derr := decode(a.body, &doc); derr != nil || doc.MediaContainer.MachineIdentifier == "" {
			add(Line{Key: "reachable", Code: CodeBadAnswer, Detail: base.Host + " answered, but not as a Plex server (no machineIdentifier)", LatencyMS: ms(a.latency)})
		} else {
			id = doc.MediaContainer
			d := fmt.Sprintf("%s answered in %d ms (server %s", base.Host, a.latency.Milliseconds(), short(id.MachineIdentifier))
			if id.Version != "" {
				d += ", Plex " + id.Version
			}
			add(Line{Key: "reachable", OK: true, Detail: d + ")", LatencyMS: ms(a.latency)})
		}
	}
	if id.MachineIdentifier == "" {
		skip(append([]string{"token"}, after...)...)
		finish(&res, "Plex is ready")
		p.logResult(res)
		return res, nil
	}

	// token: /library/sections with the token
	if token == "" {
		add(Line{Key: "token", Code: tokenCode, Detail: tokenProblem})
		skip(after...)
		finish(&res, "Plex is ready")
		p.logResult(res)
		return res, nil
	}
	hdr := map[string]string{"X-Plex-Token": token}
	var sections []plexSection
	a, err = p.get(ctx, c, base, "/library/sections", hdr)
	tokenOK := false
	switch {
	case err != nil:
		code, d := classify(err, base, p.timeout())
		add(Line{Key: "token", Code: code, Detail: d})
	case a.status == http.StatusUnauthorized || a.status == http.StatusForbidden:
		add(Line{Key: "token", Code: CodeTokenRejected, Detail: "token rejected — Sign in with Plex again", LatencyMS: ms(a.latency)})
	case a.status >= 400:
		add(Line{Key: "token", Code: CodeHTTP, Detail: fmt.Sprintf("/library/sections answered HTTP %d", a.status), LatencyMS: ms(a.latency)})
	default:
		var doc plexContainer[plexSections]
		if derr := decode(a.body, &doc); derr != nil {
			add(Line{Key: "token", Code: CodeBadAnswer, Detail: "/library/sections: " + derr.Error(), LatencyMS: ms(a.latency)})
		} else {
			tokenOK = true
			sections = doc.MediaContainer.Directory
			add(Line{Key: "token", OK: true, Detail: fmt.Sprintf("accepted: %d %s", len(sections), plural(len(sections), "library", "libraries")), LatencyMS: ms(a.latency)})
		}
	}
	if !tokenOK {
		skip(after...)
		finish(&res, "Plex is ready")
		p.logResult(res)
		return res, nil
	}

	add(p.serverLine(ctx, c, base, hdr, id))
	for _, l := range p.liveTVLines(ctx, c, base, hdr) {
		add(l)
	}
	finish(&res, "Plex is ready")
	p.logResult(res)
	return res, nil
}

func (p *Plex) logResult(res Result) {
	failed := []string{}
	for _, l := range res.Lines {
		if !l.OK && !l.Skipped {
			failed = append(failed, l.Key+"="+l.Code)
		}
	}
	p.log().Info("extensions: plex test connection", "ok", res.OK, "failed", strings.Join(failed, ","),
		"event", "extension_test")
}

// token opens the saved Plex token; "" with a code and a sentence when it
// cannot be had.
func (p *Plex) token(ctx context.Context) (string, string, string) {
	if p.Secrets == nil {
		return "", CodeSecrets, "the secrets store is not available on this server"
	}
	b, err := p.Secrets.Get(ctx, PlexTokenSecret)
	switch {
	case errors.Is(err, secrets.ErrNotFound):
		return "", CodeNoToken, "no Plex token saved — Sign in with Plex"
	case errors.Is(err, secrets.ErrWrongKey):
		return "", CodeSecrets, "the saved token cannot be opened (secrets.key was replaced) — Sign in with Plex again"
	case errors.Is(err, secrets.ErrNoKey):
		return "", CodeSecrets, "the secrets store is not available on this server"
	case err != nil:
		return "", CodeSecrets, "the saved token cannot be read"
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return "", CodeNoToken, "no Plex token saved — Sign in with Plex"
	}
	return t, "", ""
}

func (p *Plex) serverLine(ctx context.Context, c *http.Client, base *url.URL, hdr map[string]string, id plexIdentity) Line {
	a, err := p.get(ctx, c, base, "/", hdr)
	if err != nil {
		code, d := classify(err, base, p.timeout())
		return Line{Key: "server", Optional: true, Code: code, Detail: d}
	}
	if a.status >= 400 {
		return Line{Key: "server", Optional: true, Code: CodeHTTP, Detail: fmt.Sprintf("/ answered HTTP %d", a.status), LatencyMS: ms(a.latency)}
	}
	var doc plexContainer[plexRoot]
	if derr := decode(a.body, &doc); derr != nil {
		return Line{Key: "server", Optional: true, Code: CodeBadAnswer, Detail: derr.Error(), LatencyMS: ms(a.latency)}
	}
	r := doc.MediaContainer
	name := r.FriendlyName
	if name == "" {
		name = "(unnamed)"
	}
	v := r.Version
	if v == "" {
		v = id.Version
	}
	d := name
	if v != "" {
		d += ", Plex Media Server " + v
	}
	if r.Platform != "" {
		d += " on " + r.Platform
	}
	return Line{Key: "server", OK: true, Optional: true, Detail: d, LatencyMS: ms(a.latency)}
}

// liveTVLines: livetv (the DVRs), maktaba_tuner. Any other tuner in Plex Live TV is just
// another tuner: it is not checked.
func (p *Plex) liveTVLines(ctx context.Context, c *http.Client, base *url.URL, hdr map[string]string) []Line {
	skipTuners := func(why string) []Line {
		return []Line{{Key: "maktaba_tuner", Optional: true, Skipped: true, Detail: why}}
	}
	a, err := p.get(ctx, c, base, "/livetv/dvrs", hdr)
	if err != nil {
		code, d := classify(err, base, p.timeout())
		return append([]Line{{Key: "livetv", Optional: true, Code: code, Detail: d}}, skipTuners("not checked")...)
	}
	if a.status == http.StatusNotFound || a.status == http.StatusForbidden || a.status == http.StatusUnauthorized {
		return append([]Line{{Key: "livetv", Optional: true, Code: CodeLiveTVMissing, LatencyMS: ms(a.latency),
			Detail: fmt.Sprintf("Live TV is not available on this server (HTTP %d): it needs Plex Pass on the server's owner account", a.status)}},
			skipTuners("not checked")...)
	}
	if a.status >= 400 {
		return append([]Line{{Key: "livetv", Optional: true, Code: CodeHTTP, LatencyMS: ms(a.latency),
			Detail: fmt.Sprintf("/livetv/dvrs answered HTTP %d", a.status)}}, skipTuners("not checked")...)
	}
	var doc plexContainer[plexDvrs]
	if derr := decode(a.body, &doc); derr != nil {
		return append([]Line{{Key: "livetv", Optional: true, Code: CodeBadAnswer, Detail: "/livetv/dvrs: " + derr.Error(), LatencyMS: ms(a.latency)}},
			skipTuners("not checked")...)
	}
	dvrs := doc.MediaContainer.Dvr
	out := []Line{}
	if len(dvrs) == 0 {
		out = append(out, Line{Key: "livetv", Optional: true, Code: CodeLiveTVMissing, LatencyMS: ms(a.latency),
			Detail: "Live TV is available but no DVR is set up"})
	} else {
		out = append(out, Line{Key: "livetv", OK: true, Optional: true, LatencyMS: ms(a.latency),
			Detail: fmt.Sprintf("%d %s", len(dvrs), plural(len(dvrs), "DVR", "DVRs"))})
	}

	deviceID := p.hdhrDeviceID(ctx)
	var ours *plexDevice
	var ourDvr string
	for _, d := range dvrs {
		for i := range d.Device {
			t := d.Device[i]
			if ours == nil && deviceID != "" && strings.EqualFold(tunerID(t), deviceID) {
				ours, ourDvr = &t, d.Title
			}
		}
	}
	switch {
	case ours != nil:
		out = append(out, Line{Key: "maktaba_tuner", OK: true, Optional: true, Detail: "registered" + inDvr(ourDvr) + " as " + deviceID})
	case deviceID == "":
		out = append(out, Line{Key: "maktaba_tuner", Optional: true, Code: CodeNotFound,
			Detail: "Maktaba's tuner is not set up yet (TV, HDHomeRun tuner)"})
	default:
		out = append(out, Line{Key: "maktaba_tuner", Optional: true, Code: CodeNotFound,
			Detail: "Maktaba's tuner " + deviceID + " is not registered in Plex Live TV (TV, Plex: Register)"})
	}
	return out
}

func inDvr(title string) string {
	if title == "" {
		return ""
	}
	return " (DVR " + title + ")"
}

// tunerID is a device's HDHomeRun DeviceID (deviceId, else the uuid's tail).
func tunerID(d plexDevice) string {
	if d.DeviceID != "" {
		return d.DeviceID
	}
	if strings.HasPrefix(d.UUID, hdhrUUIDPrefix) {
		return d.UUID[len(hdhrUUIDPrefix):]
	}
	return ""
}

func (p *Plex) hdhrDeviceID(ctx context.Context) string {
	if p.DB == nil {
		return ""
	}
	var id sql.NullString
	if err := p.DB.QueryRowContext(ctx, `SELECT device_id FROM hdhr_device WHERE id = 1`).Scan(&id); err != nil {
		return ""
	}
	return strings.TrimSpace(id.String)
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
