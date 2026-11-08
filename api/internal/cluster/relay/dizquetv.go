package relay

// dizqueTV's operations (issue #827): the home's own tv.maktaba.dizquetv writes dizqueTV
// programmes without ever holding the Plex token, and reads dizqueTV without ever seeing it.
//
// A dizqueTV programme names its picture by a Plex URL that carries the token, and dizqueTV
// stores every programme it is sent; its plex-servers hold its own copy of the token. So:
//
//   - a read is a projection: only the fields the stages use (rating keys, durations, the
//     channel's settings), and a field whose value holds a secret in any form is left out,
//     never shown redacted (a write made from it keeps the stored value);
//   - a show or a filler list is written from token-free Plex records (the plugin's
//     program_spec): the door checks every field and builds the programme itself, the
//     picture's URL with the token and the server's name included;
//   - a channel's programmes are references: to a programme of a stored show (customShowId
//     and ratingKey), or to one the stored channel airs now (maktabaRef, which the channel's
//     projection gives each programme). The door fills each from what dizqueTV holds, on the
//     home; a programme that plays from Plex is never taken from the plugin as sent;
//   - every answer is checked, last, for every form of every secret, and withheld if one is
//     there (a projection should never hold one: this is the fail-closed guard).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The door's own dizqueTV operations (Rule.Op).
const (
	opRaw          = "raw"
	opChannels     = "channels"
	opList         = "list"
	opServers      = "servers"
	opReadList     = "read_list"
	opReadChannel  = "read_channel"
	opWriteList    = "write_list"
	opWriteChannel = "write_channel"
	opArt          = "art"
)

// progFields are what a programme's projection holds: what the stages read (the rating key,
// the duration, the custom show it belongs to) and what dizqueTV's time-slot tool orders by.
var progFields = []string{
	"ratingKey", "key", "type", "title", "showTitle", "season", "episode", "duration", "durationStr",
	"customShowId", "customShowName", "customOrder", "isOffline", "serverKey", "date", "year", "rating",
}

// chanFields are a channel's settings as the projection shows them and as the plugin may
// write them (programs apart). fallback is never shown (its clips are programmes); a
// field left out of the projection keeps its stored value on a write.
var chanFields = map[string]bool{
	"number": true, "name": true, "groupTitle": true, "icon": true, "iconWidth": true, "iconDuration": true,
	"iconPosition": true, "disableFillerOverlay": true, "guideMinimumDurationSeconds": true,
	"guideMaxPaddingSeconds": true, "guideFlexPlaceholder": true, "offlineMode": true, "offlinePicture": true,
	"offlineSoundtrack": true, "stealth": true, "transcoding": true, "watermark": true, "onDemand": true,
	"mergeAdjacentPrograms": true, "startTime": true, "duration": true, "fillerCollections": true,
	"fillerRepeatCooldown": true, "scheduleBackup": true, "maktabaPlan": true,
}

// What a token-free programme record may hold, as program_spec builds it from a Plex item.
var (
	ratingKeyRe = regexp.MustCompile(`^[0-9]{1,12}$`)
	// a Plex picture: /library/metadata/<key>/thumb|art|banner[/<version>]
	plexPicRe = regexp.MustCompile(`^/library/metadata/[0-9]{1,12}/(thumb|art|banner)(/[0-9]{1,12})?$`)
	// a Plex media part: dizqueTV streams <server's address><part> with its own token
	plexPartRe = regexp.MustCompile(`^/library/parts/[0-9]{1,12}(/[0-9]{1,12})?/file(\.[A-Za-z0-9]{1,8})?$`)
	// one file name under dz_art_url, as hosted_link quotes it
	winDriveRe = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
	artNameRe  = regexp.MustCompile(`^[A-Za-z0-9._~%-]{1,255}$`)
	showIDRe   = regexp.MustCompile(`^` + dzID + `$`)
)

// specFields: each field of a programme record and whether it must be there.
var specFields = map[string]bool{
	"title": false, "key": true, "ratingKey": true, "icon": false, "type": true, "duration": true,
	"durationStr": false, "summary": false, "rating": false, "date": false, "year": false,
	"plexFile": true, "file": true, "showTitle": false, "season": false, "episode": false, "commercials": false,
}

// playsFromPlex are the fields that make dizqueTV stream from Plex with its own token: a
// programme with one is only ever one the home built or stored.
var playsFromPlex = []string{"ratingKey", "key", "plexFile", "file", "serverKey"}

// dzCall is one operation's state.
type dzCall struct {
	d     *Door
	ctx   context.Context
	base  string // dizqueTV's address
	plex  Upstream
	forms []string // every form of every secret, never in an answer
	shows map[string][]map[string]any
	limit int64
}

func (d *Door) serveOp(ctx context.Context, s Settings, ext *Extension, rule *Rule, upath, rawQuery string,
	req Request, sink Sink) Result {
	if ext.Name != DizqueTV.Name {
		return Result{Err: fail(CodeNotAllowed, "no operation for "+ext.Name)}
	}
	timeout := time.Duration(s.TimeoutSec) * time.Second
	if req.Timeout > 0 && req.Timeout < timeout {
		timeout = req.Timeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := &dzCall{d: d, ctx: cctx, shows: map[string][]map[string]any{}, limit: int64(s.MaxResponseMB) << 20}
	if rule.Op == opArt {
		return c.art(rawQuery)
	}
	up, err := d.upstream(ctx, ext)
	if err != nil || !up.Enabled || up.URL == "" {
		why := "the extension is not set up on the home"
		if err == nil && !up.Enabled && up.URL != "" {
			why = "the extension is switched off on the home"
		}
		return Result{Err: fail(CodeNotConfigured, why)}
	}
	if c.base, err = baseURL(up.URL); err != nil {
		return Result{Err: fail(CodeNotConfigured, "the extension's address is not usable")}
	}
	plexExt := d.cfg.Extensions[Plex.Name]
	if plexExt == nil {
		plexExt = &Plex
	}
	if c.plex, err = d.upstream(ctx, plexExt); err != nil {
		c.plex = Upstream{}
	}
	if e := c.secrets(); e != nil {
		return Result{Err: e}
	}

	var status int
	var out []byte
	var e *Error
	switch rule.Op {
	case opRaw:
		status, out, e = c.raw(rule.Method, upath, rawQuery, req)
	case opChannels:
		status, out, e = c.channels()
	case opList:
		status, out, e = c.list(strings.TrimSuffix(strings.TrimPrefix(upath, "/maktaba/"), "s"))
	case opServers:
		status, out, e = c.servers()
	case opReadList:
		status, out, e = c.readList(upath)
	case opReadChannel:
		status, out, e = c.readChannel(upath)
	case opWriteList:
		status, out, e = c.writeList(rule.Method, upath, req.Body)
	case opWriteChannel:
		status, out, e = c.writeChannel(rule.Method, req.Body)
	default:
		e = fail(CodeNotAllowed, "unknown operation")
	}
	if e != nil {
		return Result{Err: e}
	}
	if c.leaks(out) {
		// a projection never holds a secret; if one ever does, nothing goes out
		return Result{Status: 0, Err: fail(CodeNotAllowed, "the answer would carry a secret: withheld")}
	}
	res := Result{Status: status, ContentType: "application/json"}
	if len(out) > 0 {
		if err := sink.Chunk(ctx, out); err != nil {
			return Result{Err: fail(CodeCancelled, "")}
		}
		res.Bytes = int64(len(out))
	}
	return res
}

// secrets gathers every form of the home's Plex token, dizqueTV's own copies of it and the
// Plex server's address: none of them is ever in an answer.
func (c *dzCall) secrets() *Error {
	toks := []string{c.plex.Secret}
	got, err := c.d.dizqueTokens(c)
	if err != nil {
		return err
	}
	toks = append(toks, got...)
	for _, t := range toks {
		if len(t) < 8 {
			continue // too short to be one; the parameter rule still applies
		}
		for _, f := range []string{t, url.QueryEscape(t), url.PathEscape(t)} {
			// a part of it is a form too: every window of eight characters
			for i := 0; i+8 <= len(f); i++ {
				c.forms = append(c.forms, f[i:i+8])
			}
		}
	}
	if u, perr := url.Parse(strings.TrimSpace(c.plex.URL)); perr == nil && u.Host != "" {
		c.forms = append(c.forms, u.Host)
	}
	return nil
}

// dizqueTokens are the access tokens dizqueTV keeps for its Plex servers (cached briefly).
func (d *Door) dizqueTokens(c *dzCall) ([]string, *Error) {
	d.mu.Lock()
	cv := d.dzTokens
	d.mu.Unlock()
	if !cv.at.IsZero() && d.now().Sub(cv.at) < cacheTTL {
		return cv.val, nil
	}
	status, raw, e := c.get("/api/plex-servers")
	if e != nil {
		return nil, e
	}
	// fail closed: without dizqueTV's own copies of the token no answer can be checked, so
	// an unreadable list refuses the call (and nothing is cached)
	if status >= 300 {
		return nil, fail(CodeUnreachable, "dizqueTV's Plex servers are unreadable")
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fail(CodeUnreachable, "dizqueTV's Plex servers are unreadable")
	}
	toks := []string{}
	for _, s := range list {
		for k, v := range s {
			if str, ok := v.(string); ok && strings.Contains(strings.ToLower(k), "token") {
				toks = append(toks, str)
			}
		}
	}
	d.mu.Lock()
	d.dzTokens = cached[[]string]{at: d.now(), val: toks}
	d.mu.Unlock()
	return toks, nil
}

// secret: s holds a form of a secret, or a token parameter.
func (c *dzCall) secret(s string) bool {
	if strings.Contains(strings.ToLower(s), "x-plex-token") {
		return true
	}
	for _, f := range c.forms {
		if strings.Contains(s, f) {
			return true
		}
	}
	return false
}

func (c *dzCall) leaks(b []byte) bool { return len(b) > 0 && c.secret(string(b)) }

// call makes one request to dizqueTV (no secret is added: dizqueTV has none).
func (c *dzCall) call(method, path string, body []byte, ctype string) (int, []byte, *Error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	hreq, err := http.NewRequestWithContext(c.ctx, method, c.base+path, rd)
	if err != nil {
		return 0, nil, fail(CodeBadRequest, "request not buildable")
	}
	hreq.Header.Set("Accept", "application/json")
	if body != nil {
		if ctype == "" || !acceptRe.MatchString(ctype) {
			ctype = "application/json"
		}
		hreq.Header.Set("Content-Type", ctype)
	}
	resp, err := c.d.client.Do(hreq)
	if err != nil {
		if errors.Is(c.ctx.Err(), context.DeadlineExceeded) {
			return 0, nil, fail(CodeTimeout, "dizqueTV did not answer in time")
		}
		return 0, nil, fail(CodeUnreachable, clip(ScrubString(DizqueTV.QueryParam, c.plex.Secret, err.Error()), 200))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.limit+1))
	if err != nil {
		return 0, nil, fail(CodeUnreachable, "dizqueTV's answer was cut short")
	}
	if int64(len(raw)) > c.limit {
		return 0, nil, fail(CodeTooLarge, "dizqueTV's answer is over the limit")
	}
	return resp.StatusCode, raw, nil
}

func (c *dzCall) get(path string) (int, []byte, *Error) { return c.call(http.MethodGet, path, nil, "") }

// getJSON reads a document; a status of 300 or more is passed on, with no body.
func (c *dzCall) getJSON(path string, into any) (int, *Error) {
	status, raw, e := c.get(path)
	if e != nil || status >= 300 {
		return status, e
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return 0, fail(CodeUnreachable, "dizqueTV answered "+path+" with no JSON")
	}
	return status, nil
}

func encode(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// ---------------------------------------------------------------- raw calls

func (c *dzCall) raw(method, path, rawQuery string, req Request) (int, []byte, *Error) {
	target := path
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	var body []byte
	if len(req.Body) > 0 {
		body = req.Body
	}
	status, out, e := c.call(method, target, body, req.ContentType)
	if e != nil {
		return 0, nil, e
	}
	if status >= 300 {
		return status, nil, nil // an error page may echo what dizqueTV holds: never passed on
	}
	// these answer with what the plugin sent (the time-slot tool), a link or nothing: scrubbed
	// all the same, and the last check withholds an answer that still holds a secret
	text := ScrubString(DizqueTV.QueryParam, c.plex.Secret, string(out))
	return status, []byte(text), nil
}

// ---------------------------------------------------------------- projections

// scalar keeps a JSON scalar whose text holds no secret.
func (c *dzCall) scalar(v any) (any, bool) {
	switch x := v.(type) {
	case nil, bool, float64:
		return x, true
	case string:
		if c.secret(x) {
			return nil, false
		}
		return x, true
	}
	return nil, false
}

func (c *dzCall) projectProg(p map[string]any, ref int) map[string]any {
	out := map[string]any{}
	for _, k := range progFields {
		if v, ok := p[k]; ok {
			if s, keep := c.scalar(v); keep {
				out[k] = s
			}
		}
	}
	if ref >= 0 {
		out["maktabaRef"] = ref
	}
	return out
}

func (c *dzCall) channels() (int, []byte, *Error) {
	var list []any
	status, e := c.getJSON("/api/channels", &list)
	if e != nil || status >= 300 {
		return status, nil, e
	}
	nums := []int{}
	for _, x := range list {
		switch v := x.(type) {
		case float64:
			nums = append(nums, int(v))
		case map[string]any:
			if n, ok := v["number"].(float64); ok {
				nums = append(nums, int(n))
			}
		}
	}
	return status, encode(nums), nil
}

func (c *dzCall) list(kind string) (int, []byte, *Error) {
	var list []map[string]any
	status, e := c.getJSON("/api/"+kind+"s", &list)
	if e != nil || status >= 300 {
		return status, nil, e
	}
	out := []map[string]any{}
	for _, x := range list {
		id, ok := c.scalar(x["id"])
		if !ok || id == nil {
			continue
		}
		item := map[string]any{"id": id}
		if name, ok := c.scalar(x["name"]); ok {
			item["name"] = name
		}
		out = append(out, item)
	}
	return status, encode(out), nil
}

// servers: each Plex server dizqueTV knows, by name, and whether it is the home's own Plex
// (its address is the home's Plex setting); never an address or a token.
func (c *dzCall) servers() (int, []byte, *Error) {
	var list []map[string]any
	status, e := c.getJSON("/api/plex-servers", &list)
	if e != nil || status >= 300 {
		return status, nil, e
	}
	want := strings.ToLower(strings.TrimRight(strings.TrimSpace(c.plex.URL), "/"))
	out := []map[string]any{}
	for _, s := range list {
		name, ok := c.scalar(s["name"])
		if !ok || name == nil {
			continue
		}
		uri, _ := s["uri"].(string)
		out = append(out, map[string]any{"name": name,
			"home": want != "" && strings.ToLower(strings.TrimRight(uri, "/")) == want})
	}
	return status, encode(out), nil
}

func (c *dzCall) readList(upath string) (int, []byte, *Error) {
	rest := strings.TrimPrefix(upath, "/maktaba/")
	var doc map[string]any
	status, e := c.getJSON("/api/"+rest, &doc)
	if e != nil || status >= 300 {
		return status, nil, e
	}
	out := map[string]any{}
	for _, k := range []string{"id", "name", "mode"} {
		if v, ok := doc[k]; ok {
			if s, keep := c.scalar(v); keep {
				out[k] = s
			}
		}
	}
	content := []map[string]any{}
	items, _ := doc["content"].([]any)
	for _, it := range items {
		if p, ok := it.(map[string]any); ok {
			content = append(content, c.projectProg(p, -1))
		}
	}
	out["content"] = content
	return status, encode(out), nil
}

// projectChannel: the channel's settings, each left out when its value holds a secret
// anywhere; with programs, each programme projected with its place (maktabaRef).
func (c *dzCall) projectChannel(doc map[string]any, programs bool) map[string]any {
	out := map[string]any{}
	for k, v := range doc {
		if !chanFields[k] {
			continue
		}
		if c.secret(string(encode(v))) {
			continue
		}
		out[k] = v
	}
	if programs {
		progs := []map[string]any{}
		items, _ := doc["programs"].([]any)
		for i, it := range items {
			if p, ok := it.(map[string]any); ok {
				progs = append(progs, c.projectProg(p, i))
			} else {
				progs = append(progs, map[string]any{"maktabaRef": i})
			}
		}
		out["programs"] = progs
	}
	return out
}

func (c *dzCall) readChannel(upath string) (int, []byte, *Error) {
	rest := strings.TrimPrefix(upath, "/maktaba/")
	var doc map[string]any
	status, e := c.getJSON("/api/"+rest, &doc)
	if e != nil || status >= 300 {
		return status, nil, e
	}
	return status, encode(c.projectChannel(doc, !strings.HasPrefix(rest, "channel/programless/"))), nil
}

// ---------------------------------------------------------------- writes

func bad(format string, a ...any) *Error { return fail(CodeBadRequest, fmt.Sprintf(format, a...)) }

func decodeObject(body []byte) (map[string]any, *Error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, bad("the body is not a JSON object")
	}
	return m, nil
}

// mediaPath: an absolute path on a media server's disk (POSIX, a drive or a share), as Plex
// reports a part's file; never a URL (dizqueTV's ffmpeg would fetch it), never a climb.
func mediaPath(s string) bool {
	if !strings.HasPrefix(s, "/") && !winDriveRe.MatchString(s) && !strings.HasPrefix(s, `\\`) {
		return false
	}
	if strings.Contains(s, "://") || strings.ContainsRune(s, '\x00') {
		return false
	}
	for _, seg := range strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return false
		}
	}
	return true
}

func plainText(v any, limit int) (string, bool) {
	s, ok := v.(string)
	if !ok || len(s) > limit || !utf8.ValidString(s) {
		return "", false
	}
	for _, r := range s {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return "", false
		}
	}
	return s, true
}

func wholeNumber(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	if err != nil {
		f, ferr := n.Float64()
		if ferr != nil || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
			return 0, false
		}
		i = int64(f)
	}
	return i, true
}

// homeServer is the name dizqueTV registered the home's Plex under (its address is the
// home's Plex setting), else its only server: what every programme names (serverKey).
func (c *dzCall) homeServer() (string, *Error) {
	var list []map[string]any
	status, e := c.getJSON("/api/plex-servers", &list)
	if e != nil {
		return "", e
	}
	if status >= 300 {
		return "", fail(CodeUnreachable, "dizqueTV did not list its Plex servers")
	}
	want := strings.ToLower(strings.TrimRight(strings.TrimSpace(c.plex.URL), "/"))
	for _, s := range list {
		uri, _ := s["uri"].(string)
		if name, ok := s["name"].(string); ok && strings.ToLower(strings.TrimRight(uri, "/")) == want {
			return name, nil
		}
	}
	if len(list) == 1 {
		if name, ok := list[0]["name"].(string); ok {
			return name, nil
		}
	}
	return "", fail(CodeNotConfigured, "dizqueTV has no Plex server registered at the home's Plex address")
}

// build makes the programme from a token-free record: every field checked, the picture's URL
// (Plex's address, the path, the token) and the server's name added here.
func (c *dzCall) build(spec map[string]any, server, plexBase string, i int) (map[string]any, *Error) {
	for k := range spec {
		if _, ok := specFields[k]; !ok {
			return nil, bad("programme %d: %q is not a field the door builds from", i, clip(k, 40))
		}
	}
	for k, req := range specFields {
		if _, ok := spec[k]; req && !ok {
			return nil, bad("programme %d: %s is missing", i, k)
		}
	}
	p := map[string]any{}
	rk, ok := spec["ratingKey"].(string)
	if !ok || !ratingKeyRe.MatchString(rk) {
		return nil, bad("programme %d: the rating key is not a Plex item's", i)
	}
	p["ratingKey"] = rk
	if spec["key"] != "/library/metadata/"+rk {
		return nil, bad("programme %d: its key is not its item's", i)
	}
	p["key"] = spec["key"]
	switch spec["type"] {
	case "movie", "episode":
		p["type"] = spec["type"]
	default:
		return nil, bad("programme %d: not a movie or an episode", i)
	}
	d, ok := wholeNumber(spec["duration"])
	if !ok || d <= 0 {
		return nil, bad("programme %d: no duration", i)
	}
	p["duration"] = d
	part, ok := spec["plexFile"].(string)
	if !ok || !plexPartRe.MatchString(part) {
		return nil, bad("programme %d: its part is not a Plex media part", i)
	}
	p["plexFile"] = part
	file, ok := plainText(spec["file"], 4096)
	if !ok || !mediaPath(file) {
		return nil, bad("programme %d: its file is not a path", i)
	}
	p["file"] = file
	for _, k := range []string{"title", "summary", "showTitle", "rating", "date", "durationStr"} {
		v, there := spec[k]
		if !there {
			continue
		}
		if v == nil {
			p[k] = nil
			continue
		}
		limit := 64 << 10
		if k == "date" || k == "durationStr" || k == "rating" {
			limit = 64
		}
		s, ok := plainText(v, limit)
		if !ok {
			return nil, bad("programme %d: %s is not text", i, k)
		}
		p[k] = s
	}
	for _, k := range []string{"year", "season", "episode"} {
		v, there := spec[k]
		if !there {
			continue
		}
		if v == nil {
			p[k] = nil
			continue
		}
		n, ok := wholeNumber(v)
		if !ok {
			return nil, bad("programme %d: %s is not a number", i, k)
		}
		p[k] = n
	}
	if cm, there := spec["commercials"]; there {
		if l, ok := cm.([]any); !ok || len(l) != 0 {
			return nil, bad("programme %d: commercials are not built through the door", i)
		}
	}
	// the picture: a Plex path only; the token is added here and never leaves the home
	p["icon"] = nil
	if pic, ok := spec["icon"].(string); ok && plexPicRe.MatchString(pic) {
		p["icon"] = plexBase + pic + "?X-Plex-Token=" + c.plex.Secret
	}
	p["serverKey"] = server
	p["commercials"] = []any{}
	return p, nil
}

// writeList writes a show or a filler list whose programmes the door builds.
func (c *dzCall) writeList(method, upath string, body []byte) (int, []byte, *Error) {
	rest := strings.TrimPrefix(upath, "/maktaba/")
	kind, id, _ := strings.Cut(rest, "/")
	doc, e := decodeObject(body)
	if e != nil {
		return 0, nil, e
	}
	out := map[string]any{}
	for k, v := range doc {
		switch k {
		case "name":
			s, ok := plainText(v, 512)
			if !ok || s == "" {
				return 0, nil, bad("the name is not text")
			}
			out["name"] = s
		case "mode":
			if kind != "filler" || v != "custom" {
				return 0, nil, bad("only a filler list's mode, custom, is written")
			}
			out["mode"] = v
		case "content":
		default:
			return 0, nil, bad("%q is not a field of a %s the door writes", clip(k, 40), kind)
		}
	}
	if _, ok := out["name"]; !ok {
		return 0, nil, bad("no name")
	}
	items, ok := doc["content"].([]any)
	if !ok {
		return 0, nil, bad("no content")
	}
	if c.plex.Secret == "" || strings.TrimSpace(c.plex.URL) == "" || !c.plex.Enabled {
		return 0, nil, fail(CodeNotConfigured, "writing dizqueTV programmes needs the home's Plex (address and token)")
	}
	server, e := c.homeServer()
	if e != nil {
		return 0, nil, e
	}
	plexBase := strings.TrimRight(strings.TrimSpace(c.plex.URL), "/")
	content := make([]any, 0, len(items))
	for i, it := range items {
		spec, ok := it.(map[string]any)
		if !ok {
			return 0, nil, bad("programme %d is not an object", i)
		}
		p, e := c.build(spec, server, plexBase, i)
		if e != nil {
			return 0, nil, e
		}
		content = append(content, p)
	}
	out["content"] = content
	path := "/api/" + kind
	if id != "" {
		path += "/" + id
	}
	status, raw, e := c.call(method, path, encode(out), "application/json")
	if e != nil || status >= 300 {
		return status, nil, e
	}
	if id != "" {
		return status, encode(map[string]any{}), nil
	}
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	nid, ok := c.scalar(got["id"])
	if !ok || nid == nil {
		return 0, nil, fail(CodeUnreachable, "dizqueTV gave the new "+kind+" no id")
	}
	return status, encode(map[string]any{"id": nid}), nil
}

func keyText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

// showContent is a stored show's programmes (read once per operation).
func (c *dzCall) showContent(id string) ([]map[string]any, *Error) {
	if got, ok := c.shows[id]; ok {
		return got, nil
	}
	if !showIDRe.MatchString(id) {
		return nil, bad("%q is not a show's id", clip(id, 70))
	}
	var doc map[string]any
	status, e := c.getJSON("/api/show/"+id, &doc)
	if e != nil {
		return nil, e
	}
	if status == http.StatusNotFound {
		return nil, bad("no stored show %s", id)
	}
	if status >= 300 {
		return nil, fail(CodeUnreachable, fmt.Sprintf("dizqueTV answered show %s with %d", id, status))
	}
	var out []map[string]any
	items, _ := doc["content"].([]any)
	for _, it := range items {
		if p, ok := it.(map[string]any); ok {
			out = append(out, p)
		}
	}
	c.shows[id] = out
	return out, nil
}

// fill makes one programme of a channel from what dizqueTV holds.
func (c *dzCall) fill(e map[string]any, stored []any, i int) (any, *Error) {
	if ref, there := e["maktabaRef"]; there {
		n, ok := wholeNumber(ref)
		if !ok || n < 0 || int(n) >= len(stored) {
			return nil, bad("programme %d: no programme %v on the stored channel", i, ref)
		}
		p, ok := stored[n].(map[string]any)
		if !ok {
			return stored[n], nil
		}
		if keyText(p["ratingKey"]) != keyText(e["ratingKey"]) {
			return nil, bad("programme %d: the stored channel airs another item there (it changed: read it again)", i)
		}
		return p, nil
	}
	if rk, there := e["ratingKey"]; there {
		sid, ok := e["customShowId"].(string)
		if !ok {
			return nil, bad("programme %d: a programme of no custom show", i)
		}
		content, err := c.showContent(sid)
		if err != nil {
			return nil, err
		}
		for _, p := range content {
			if keyText(p["ratingKey"]) == keyText(rk) {
				out := make(map[string]any, len(p)+3)
				for k, v := range p {
					out[k] = v
				}
				out["customShowId"] = sid
				if name, there := e["customShowName"]; there {
					s, ok := plainText(name, 512)
					if !ok {
						return nil, bad("programme %d: customShowName is not text", i)
					}
					out["customShowName"] = s
				}
				if order, there := e["customOrder"]; there {
					o, ok := wholeNumber(order)
					if !ok {
						return nil, bad("programme %d: customOrder is not a number", i)
					}
					out["customOrder"] = o
				}
				return out, nil
			}
		}
		return nil, bad("programme %d: show %s holds no item %s", i, sid, clip(keyText(rk), 20))
	}
	// filler time, a live broadcast: nothing that plays from Plex
	for _, k := range playsFromPlex {
		if _, there := e[k]; there {
			return nil, bad("programme %d: %s is only ever filled by the door", i, k)
		}
	}
	return e, nil
}

// writeChannel writes a channel whose programmes are references, filled on the home; the
// fields the projection left out keep their stored values.
func (c *dzCall) writeChannel(method string, body []byte) (int, []byte, *Error) {
	doc, e := decodeObject(body)
	if e != nil {
		return 0, nil, e
	}
	for k, v := range doc {
		switch {
		case k == "programs":
		case k == "fallback":
			if l, ok := v.([]any); !ok || len(l) != 0 {
				return 0, nil, bad("a channel's fallback is never written through the door")
			}
		case !chanFields[k]:
			return 0, nil, bad("%q is not a field of a channel the door writes", clip(k, 40))
		}
	}
	num, ok := wholeNumber(doc["number"])
	if !ok || num < 1 || num > 999999 {
		return 0, nil, bad("no channel number")
	}
	progs, ok := doc["programs"].([]any)
	if !ok {
		return 0, nil, bad("no programs")
	}
	var stored map[string]any
	status, e := c.getJSON("/api/channel/"+strconv.FormatInt(num, 10), &stored)
	if e != nil {
		return 0, nil, e
	}
	if status >= 300 {
		if status != http.StatusNotFound {
			return 0, nil, fail(CodeUnreachable, fmt.Sprintf("dizqueTV answered channel %d with %d", num, status))
		}
		stored = nil
	}
	var storedProgs []any
	if stored != nil {
		storedProgs, _ = stored["programs"].([]any)
	}
	filled := make([]any, 0, len(progs))
	for i, it := range progs {
		p, ok := it.(map[string]any)
		if !ok {
			return 0, nil, bad("programme %d is not an object", i)
		}
		got, e := c.fill(p, storedProgs, i)
		if e != nil {
			return 0, nil, e
		}
		filled = append(filled, got)
	}
	out := map[string]any{}
	for k, v := range stored {
		if k != "programs" {
			out[k] = v
		}
	}
	for k, v := range doc {
		if k != "programs" {
			out[k] = v
		}
	}
	out["programs"] = filled
	status, _, e = c.call(method, "/api/channel", encode(out), "application/json")
	if e != nil || status >= 300 {
		return status, nil, e
	}
	return status, encode(map[string]any{}), nil
}

// ---------------------------------------------------------------- the hosted-logo check

// artName: the one file directly under base that link names (no climb, no encoded
// separator), or "" when link names anything else.
func artName(base, link string) string {
	name, ok := strings.CutPrefix(link, base+"/")
	low := strings.ToLower(name)
	if !ok || !artNameRe.MatchString(name) || strings.HasPrefix(name, ".") ||
		strings.Contains(low, "%2f") || strings.Contains(low, "%5c") || strings.Contains(low, "%2e") {
		return ""
	}
	return name
}

// art answers the status of a HEAD of one file under the home's own dz_art_url (no body):
// the only web host this plugin may reach, set by an admin, never the plugin's choice.
func (c *dzCall) art(rawQuery string) Result {
	vals, _ := url.ParseQuery(rawQuery)
	link := vals.Get("url")
	base := ""
	if c.d.cfg.ArtBase != nil {
		got, err := c.d.cfg.ArtBase(c.ctx)
		if err != nil {
			return Result{Err: fail(CodeNotConfigured, "dz_art_url unreadable")}
		}
		base = strings.TrimRight(strings.TrimSpace(got), "/")
	}
	if base == "" {
		return Result{Err: fail(CodeNotConfigured, "no logo host (dz_art_url) is set on the home")}
	}
	name := artName(base, link)
	if name == "" {
		return Result{Err: fail(CodeNotAllowed, "only a file under the home's logo host is checked")}
	}
	// the request goes to the admin's host with the checked file name, never to the string
	// the plugin sent
	hreq, err := http.NewRequestWithContext(c.ctx, http.MethodHead, base+"/"+name, nil)
	if err != nil {
		return Result{Err: fail(CodeBadRequest, "request not buildable")}
	}
	// no secret goes with it: a logo host's redirect may be followed (as the worker's own
	// check did), a few times at most and only to another file under the same host; any
	// other hop answers the redirect's own status
	client := *c.d.client
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 3 || artName(base, r.URL.String()) == "" {
			return http.ErrUseLastResponse
		}
		return nil
	}
	resp, err := client.Do(hreq)
	if err != nil {
		return Result{} // unreachable: status 0, as the worker's own check answers
	}
	resp.Body.Close()
	return Result{Status: resp.StatusCode}
}
