package plugins

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// marker is in every refused payload below: it must never reach a log line.
const marker = "EMIT-BODY-MARKER-7f3a"

// emitLogs points the fixture's server at a JSON log and answers a reader of its
// plugin_emit_refused records and the whole log's text.
func (f *fixture) emitLogs(t *testing.T) func() ([]map[string]any, string) {
	t.Helper()
	var buf bytes.Buffer
	f.s.Logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return func() ([]map[string]any, string) {
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			var rec map[string]any
			if json.Unmarshal([]byte(line), &rec) == nil && rec["msg"] == "plugin_emit_refused" {
				out = append(out, rec)
			}
		}
		text := buf.String()
		buf.Reset()
		return out, text
	}
}

// callRaw posts body as it is (a call that need not be JSON).
func (f *fixture) callRaw(t *testing.T, token, route, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/plugin-api/v1/"+route, strings.NewReader(body))
	req.Header.Set("Authorization", "Plugin "+token)
	w := httptest.NewRecorder()
	f.s.ServeHTTP(w, req)
	var out struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out.Code
}

// assertOneRefusal: exactly one plugin_emit_refused record, with the plugin, the kind, the
// payload's size and the reason, and no part of the payload anywhere in the log.
func assertOneRefusal(t *testing.T, read func() ([]map[string]any, string), plugin, kind string, size int, reason string) {
	t.Helper()
	recs, text := read()
	if len(recs) != 1 {
		t.Fatalf("want one plugin_emit_refused record, got %d: %s", len(recs), text)
	}
	r := recs[0]
	if r["plugin"] != plugin || r["kind"] != kind || r["reason"] != reason || r["size"] != float64(size) {
		t.Errorf("record %v: want plugin %q kind %q size %d reason %q", r, plugin, kind, size, reason)
	}
	if d, _ := r["detail"].(string); d == "" {
		t.Errorf("record %v has no detail", r)
	}
	if strings.Contains(text, marker) {
		t.Errorf("the payload reached the log: %s", text)
	}
}

func emitBody(f *fixture, name, payload string) map[string]any {
	return map[string]any{"name": name, "subject": map[string]string{"kind": "video", "id": f.videoA}, "payload": payload}
}

// An event the calling stage does not declare in emits.
func TestEmitLogsUnknownKind(t *testing.T) {
	f := newFixture(t)
	f.withEvents(t, true)
	read := f.emitLogs(t)
	payload := `{"note":"` + marker + `"}`
	if _, code, _ := f.call(t, f.mint(t).Token, "events/emit", emitBody(f, "other.done", payload)); code != CodeScope {
		t.Fatalf("code %s", code)
	}
	assertOneRefusal(t, read, testPlugin, "other.done", len(payload), EmitUnknownKind)
}

// A payload its schema refuses: a value out of range, and a key the schema does not name
// (the key itself is the marker, so the detail must not name it).
func TestEmitLogsSchemaMismatch(t *testing.T) {
	f := newFixture(t)
	f.withEvents(t, true)
	read := f.emitLogs(t)
	tok := f.mint(t).Token
	for _, payload := range []string{
		`{"count":500,"` + marker + `":"` + marker + `"}`,
		`{"count":1,"` + marker + `":true}`,
	} {
		if _, code, _ := f.call(t, tok, "events/emit", emitBody(f, "tagged.done", payload)); code != CodeInvalid {
			t.Fatalf("%s: code %s", payload, code)
		}
		assertOneRefusal(t, read, testPlugin, "tagged.done", len(payload), EmitSchema)
	}
}

// A payload over 16 KB, and a request over 6 MB.
func TestEmitLogsOversize(t *testing.T) {
	f := newFixture(t)
	f.withEvents(t, true)
	read := f.emitLogs(t)
	tok := f.mint(t).Token
	payload := `{"count":1,"pad":"` + strings.Repeat(marker, MaxEvent/len(marker)+1) + `"}`
	if _, code, _ := f.call(t, tok, "events/emit", emitBody(f, "tagged.done", payload)); code != CodeTooLarge {
		t.Fatalf("code %s", code)
	}
	assertOneRefusal(t, read, testPlugin, "tagged.done", len(payload), EmitOversize)

	huge, _ := json.Marshal(emitBody(f, "tagged.done", strings.Repeat(marker, MaxBody/len(marker)+1)))
	if status, code := f.callRaw(t, tok, "events/emit", string(huge)); status != 413 || code != CodeTooLarge {
		t.Fatalf("over 6 MB: %d %s", status, code)
	}
	// the server reads one byte past the cap, so the size logged is that much
	assertOneRefusal(t, read, testPlugin, "", MaxBody+1, EmitOversize)
}

// A subject of another kind than the event's, and a video outside the job's library.
func TestEmitLogsOutOfScopeSubject(t *testing.T) {
	f := newFixture(t)
	f.withEvents(t, true)
	read := f.emitLogs(t)
	tok := f.mint(t).Token
	payload := `{"count":1,"x":"` + marker + `"}`
	for _, sub := range []map[string]string{
		{"kind": "season", "id": f.videoA},
		{"kind": "video", "id": f.videoC},
	} {
		in := map[string]any{"name": "tagged.done", "subject": sub, "payload": payload}
		if _, code, _ := f.call(t, tok, "events/emit", in); code != CodeScope {
			t.Fatalf("%v: code %s", sub, code)
		}
		assertOneRefusal(t, read, testPlugin, "tagged.done", len(payload), EmitScope)
	}
}

// No token, an unknown token, and a token of a plugin whose grants changed since it was
// minted: the plugin is logged when the token names one.
func TestEmitLogsBadToken(t *testing.T) {
	f := newFixture(t)
	f.withEvents(t, true)
	read := f.emitLogs(t)
	payload := `{"count":1,"x":"` + marker + `"}`
	body, _ := json.Marshal(emitBody(f, "tagged.done", payload))

	req := httptest.NewRequest(http.MethodPost, "/plugin-api/v1/events/emit", bytes.NewReader(body))
	w := httptest.NewRecorder()
	f.s.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
	assertOneRefusal(t, read, "", "tagged.done", len(payload), EmitToken)

	if status, code := f.callRaw(t, "nope", "events/emit", string(body)); status != 401 || code != CodeToken {
		t.Fatalf("unknown token: %d %s", status, code)
	}
	assertOneRefusal(t, read, "", "tagged.done", len(payload), EmitToken)

	tok := f.mint(t).Token
	f.exec(t, `UPDATE plugin_grants SET generation = generation + 1 WHERE plugin_id = $1`, testPlugin)
	if status, code := f.callRaw(t, tok, "events/emit", string(body)); status != 401 || code != CodeGrantsChanged {
		t.Fatalf("grants changed: %d %s", status, code)
	}
	assertOneRefusal(t, read, testPlugin, "tagged.done", len(payload), EmitToken)
}

// A request that does not decode (a payload that is not a string, or not JSON at all), and
// a payload that is not a JSON object.
func TestEmitLogsUndecodablePayload(t *testing.T) {
	f := newFixture(t)
	f.withEvents(t, true)
	read := f.emitLogs(t)
	tok := f.mint(t).Token

	notString := `{"note":"` + marker + `"}`
	body := `{"name":"tagged.done","subject":{"kind":"video","id":"` + f.videoA + `"},"payload":` + notString + `}`
	if status, code := f.callRaw(t, tok, "events/emit", body); status != 400 || code != CodeInvalid {
		t.Fatalf("not a string: %d %s", status, code)
	}
	assertOneRefusal(t, read, testPlugin, "tagged.done", len(notString), EmitDecode)

	broken := `{"name":"tagged.done","payload":"` + marker
	if status, code := f.callRaw(t, tok, "events/emit", broken); status != 400 || code != CodeInvalid {
		t.Fatalf("not JSON: %d %s", status, code)
	}
	assertOneRefusal(t, read, testPlugin, "", len(broken), EmitDecode)

	notObject := `["` + marker + `"]`
	if _, code, _ := f.call(t, tok, "events/emit", emitBody(f, "tagged.done", notObject)); code != CodeInvalid {
		t.Fatalf("not an object: %s", code)
	}
	assertOneRefusal(t, read, testPlugin, "tagged.done", len(notObject), EmitDecode)
}

// The other refusals emit makes: a forged lineage, the job's budget, a loop and the rate.
func TestEmitLogsLineageQuotaLoopAndRate(t *testing.T) {
	f := newFixture(t)
	hear := f.withEvents(t, true)
	read := f.emitLogs(t)
	m := f.mint(t)
	inv := f.s.Tokens.byID[m.InvocationID]

	forged := `{"count":1,"_lineage":{"hops":"` + marker + `"}}`
	if _, code, _ := f.call(t, m.Token, "events/emit", emitBody(f, "tagged.done", forged)); code != CodeInvalid {
		t.Fatalf("lineage: %s", code)
	}
	assertOneRefusal(t, read, testPlugin, "tagged.done", len(forged), EmitLineage)

	payload := `{"count":1}`
	inv.mu.Lock()
	inv.requests = MaxRequests
	inv.mu.Unlock()
	if _, code, _ := f.call(t, m.Token, "events/emit", emitBody(f, "tagged.done", payload)); code != CodeQuota {
		t.Fatalf("quota: %s", code)
	}
	assertOneRefusal(t, read, testPlugin, "tagged.done", len(payload), EmitQuota)

	inv.mu.Lock()
	inv.bucket, inv.bucketAt = 0, f.s.Tokens.Now()
	inv.mu.Unlock()
	if _, code, _ := f.call(t, m.Token, "events/emit", emitBody(f, "tagged.done", payload)); code != CodeRate {
		t.Fatalf("rate: %s", code)
	}
	assertOneRefusal(t, read, testPlugin, "tagged.done", len(payload), EmitRate)

	key := "video:" + f.videoA
	f.exec(t, `UPDATE processing_jobs SET payload = $2 WHERE id = $1`, f.jobID,
		lineageWith(2, [2]string{hear, key}, [2]string{QueueName(testPlugin, "tag"), key}))
	m = f.mint(t)
	if _, code, _ := f.call(t, m.Token, "events/emit", emitBody(f, "tagged.done", payload)); code != CodeLoop {
		t.Fatalf("loop: %s", code)
	}
	assertOneRefusal(t, read, testPlugin, "tagged.done", len(payload), EmitLoop)
}

// An emit that goes through logs no refusal.
func TestEmitAcceptedLogsNoRefusal(t *testing.T) {
	f := newFixture(t)
	f.withEvents(t, true)
	read := f.emitLogs(t)
	if code, _, b := f.call(t, f.mint(t).Token, "events/emit", emitBody(f, "tagged.done", `{"count":3}`)); code != 200 {
		t.Fatalf("emit: %d %s", code, b)
	}
	if recs, text := read(); len(recs) != 0 {
		t.Fatalf("a refusal was logged: %s", text)
	}
}
