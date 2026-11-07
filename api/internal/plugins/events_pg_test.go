package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

const subscriber = "dev.example.listener"

// withEvents gives the fixture's plugin a published event (tagged.done, its payload
// schema in its package) emitted by its stage "tag", and a second plugin that subscribes
// to it, enabled, granted or not.
func (f *fixture) withEvents(t *testing.T, granted bool) string {
	t.Helper()
	var pkg string
	if err := f.db.QueryRow(`SELECT path FROM plugin_installs WHERE plugin_id = $1`, testPlugin).Scan(&pkg); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(pkg, "events"), 0o755)
	_ = os.WriteFile(filepath.Join(pkg, "events", "done.schema.json"), []byte(`{"type":"object","additionalProperties":false,
		"required":["count"],"properties":{"count":{"type":"integer","minimum":0,"maximum":100}}}`), 0o644)
	f.exec(t, `UPDATE plugin_installs SET manifest = $2 WHERE plugin_id = $1`, testPlugin,
		`{"resources":{"timeout_s":60,"data_mb":1},"stage":[{"name":"tag","subject":"video","emits":["tagged.done"],"may_request":["more"]},
		{"name":"more","may_request":[]}],"event":[{"name":"tagged.done","subject":"video","payload":"events/done.schema.json"}]}`)
	f.exec(t, `INSERT INTO plugin_installs (plugin_id, version, digest, path, manifest) VALUES ($1,'1.0.0','d','/x',$2)`, subscriber,
		`{"stage":[{"name":"hear","subject":"video","trigger":[{"event":"plugin:`+testPlugin+`/tagged.done"}]}],
		"depends":[{"plugin":"`+testPlugin+`","version":">=1.0.0"}]}`)
	caps := []string{"library.read"}
	if granted {
		caps = append(caps, "events.subscribe:"+testPlugin)
	}
	f.exec(t, `INSERT INTO plugin_grants (plugin_id, capabilities) VALUES ($1, $2)`, subscriber, pq.Array(caps))
	f.exec(t, `INSERT INTO components (id, want, updated_by) VALUES ($1, 'enabled', 'test')
		ON CONFLICT (id) DO UPDATE SET want = 'enabled'`,
		components.PluginPrefix+subscriber)
	return QueueName(subscriber, "hear")
}

// 54.40 T01: emit of a declared event with a payload its schema takes goes to the granted
// subscriber's stage; undeclared, wrong subject, bad payload and a forged lineage refused.
func TestEmit(t *testing.T) {
	f := newFixture(t)
	hear := f.withEvents(t, true)
	tok := f.mint(t).Token
	vid := map[string]string{"kind": "video", "id": f.videoA}
	code, _, body := f.call(t, tok, "events/emit", map[string]any{"name": "tagged.done", "subject": vid, "payload": `{"count":3}`})
	if code != 200 || !strings.Contains(string(body), hear) {
		t.Fatalf("emit: %d %s", code, body)
	}
	for _, c := range []struct {
		in   map[string]any
		code string
	}{
		{map[string]any{"name": "other.done", "subject": vid, "payload": `{}`}, CodeScope},
		{map[string]any{"name": "tagged.done", "subject": map[string]string{"kind": "season", "id": f.videoA}, "payload": `{"count":1}`}, CodeScope},
		{map[string]any{"name": "tagged.done", "subject": map[string]string{"kind": "video", "id": f.videoC}, "payload": `{"count":1}`}, CodeScope},
		{map[string]any{"name": "tagged.done", "subject": vid, "payload": `{"count":500}`}, CodeInvalid},
		{map[string]any{"name": "tagged.done", "subject": vid, "payload": `{"count":1,"extra":true}`}, CodeInvalid},
		{map[string]any{"name": "tagged.done", "subject": vid, "payload": `{"count":1,"_lineage":{"hops":1}}`}, CodeInvalid},
		{map[string]any{"name": "tagged.done", "subject": vid, "payload": strings.Repeat(" ", MaxEvent+1)}, CodeTooLarge},
	} {
		if _, got, b := f.call(t, tok, "events/emit", c.in); got != c.code {
			t.Errorf("%v: want %s, got %s %s", c.in, c.code, got, b)
		}
	}
}

// 54.40 T01: without events.subscribe the subscriber gets nothing.
func TestEmitNeedsTheSubscribersGrant(t *testing.T) {
	f := newFixture(t)
	f.withEvents(t, false)
	tok := f.mint(t).Token
	code, _, body := f.call(t, tok, "events/emit", map[string]any{"name": "tagged.done",
		"subject": map[string]string{"kind": "video", "id": f.videoA}, "payload": `{"count":3}`})
	if code != 200 || !strings.Contains(string(body), `"stages":[]`) {
		t.Fatalf("emit: %d %s", code, body)
	}
}

func lineageWith(hops int, pairs ...[2]string) string {
	l := Lineage{Origin: Origin{Kind: "core"}, Hops: hops, Path: pairs}
	b, _ := json.Marshal(map[string]any{LineageKey: l})
	return string(b)
}

// 54.40 T03, T04: a request at the hop cap, and a request or event into a pair already in
// the path, are plugin_loop; the job's result then fails as plugin_loop (the host).
func TestLineageRefusesTheHopCapAndReentry(t *testing.T) {
	f := newFixture(t)
	hear := f.withEvents(t, true)
	more := QueueName(testPlugin, "more")
	vid := map[string]string{"kind": "video", "id": f.videoA}
	key := "video:" + f.videoA
	var pairs [][2]string
	for i := 0; i < MaxHops; i++ {
		pairs = append(pairs, [2]string{fmt.Sprintf("p_x_%d", i), key})
	}
	f.exec(t, `UPDATE processing_jobs SET payload = $2 WHERE id = $1`, f.jobID, lineageWith(MaxHops, pairs...))
	m := f.mint(t)
	if m.Lineage.Hops != MaxHops {
		t.Fatalf("lineage %+v", m.Lineage)
	}
	if _, code, _ := f.call(t, m.Token, "queue/request", map[string]any{"stage": "more", "subject": vid}); code != CodeLoop {
		t.Fatalf("at the cap: %s", code)
	}
	// one hop in, but more on this video already in the path
	f.exec(t, `UPDATE processing_jobs SET payload = $2 WHERE id = $1`, f.jobID,
		lineageWith(2, [2]string{more, key}, [2]string{QueueName(testPlugin, "tag"), key}))
	m = f.mint(t)
	if _, code, _ := f.call(t, m.Token, "queue/request", map[string]any{"stage": "more", "subject": vid}); code != CodeLoop {
		t.Fatalf("re-entry by request: %s", code)
	}
	if code, _, b := f.call(t, m.Token, "queue/request", map[string]any{"stage": "more",
		"subject": map[string]string{"kind": "video", "id": f.videoB}}); code != 200 {
		t.Fatalf("the same stage on another video: %d %s", code, b)
	}
	f.exec(t, `UPDATE processing_jobs SET payload = $2 WHERE id = $1`, f.jobID,
		lineageWith(2, [2]string{hear, key}, [2]string{QueueName(testPlugin, "tag"), key}))
	m = f.mint(t)
	if _, code, _ := f.call(t, m.Token, "events/emit", map[string]any{"name": "tagged.done", "subject": vid,
		"payload": `{"count":1}`}); code != CodeLoop {
		t.Fatalf("re-entry by event: %s", code)
	}
}

// 54.40 T05: a plugin write leaves its lineage on the video; a job a core event starts
// there within 10 minutes inherits it, and is refused when its own pair is in it (a loop
// through data); after 10 minutes it starts afresh.
func TestLoopThroughData(t *testing.T) {
	f := newFixture(t)
	tok := f.mint(t).Token
	if code, _, b := f.call(t, tok, "write/add-tags", map[string]any{"video": f.videoA, "tags": []string{"Night"}}); code != 200 {
		t.Fatalf("tag: %d %s", code, b)
	}
	var hops int
	if err := f.db.QueryRow(`SELECT (lineage->>'hops')::int FROM plugin_write_lineage WHERE subject_kind = 'video' AND subject_id = $1`,
		f.videoA).Scan(&hops); err != nil || hops != 1 {
		t.Fatalf("stamp: %d %v", hops, err)
	}
	// the job ended; the core reran and its event started the same stage on the video again
	f.exec(t, `UPDATE processing_jobs SET state = 'done' WHERE id = $1`, f.jobID)
	var again int64
	if err := f.db.QueryRow(`INSERT INTO processing_jobs (stage, state, subject_kind, subject_id, video_id)
		VALUES ($1,'running','video',$2,$2) RETURNING id`, QueueName(testPlugin, "tag"), f.videoA).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Tokens.Mint(context.Background(), again); err == nil || !strings.Contains(err.Error(), CodeLoop) {
		t.Fatalf("a loop through data was minted: %v", err)
	}
	// another stage of it on the video inherits the lineage, one hop more
	more, err := f.insertJob(t, QueueName(testPlugin, "more"), f.videoA)
	if err != nil {
		t.Fatal(err)
	}
	m, err := f.s.Tokens.Mint(context.Background(), more)
	if err != nil || m.Lineage.Hops != 2 || m.Lineage.Origin.Kind != "plugin" {
		t.Fatalf("inherited: %+v %v", m, err)
	}
	// ten minutes later the stamp no longer counts
	f.s.Tokens.Now = func() time.Time { return time.Now().Add(DataWindow + time.Minute) }
	if m, err := f.s.Tokens.Mint(context.Background(), again); err != nil || m.Lineage.Hops != 1 {
		t.Fatalf("after the window: %+v %v", m, err)
	}
}

func (f *fixture) insertJob(t *testing.T, stage, video string) (int64, error) {
	t.Helper()
	var id int64
	err := f.db.QueryRow(`INSERT INTO processing_jobs (stage, state, subject_kind, subject_id, video_id)
		VALUES ($1,'running','video',$2,$2) RETURNING id`, stage, video).Scan(&id)
	return id, err
}
