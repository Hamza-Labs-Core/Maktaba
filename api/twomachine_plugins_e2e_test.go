package main

// Epic 54 story 54.42 on the two-machine rig (TestTwoMachinesOnePC): plugin A on the home
// emits an event that starts plugin B on the joined machine; B's token comes from the home,
// its read is the machine's own, its write is applied by the home with B's lineage; turning
// B off on the home reaches the machine at its next call.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"

	pl "github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
)

// pluginAPISocket is a machine's plugin API socket as its supervisor names it
// (supervisor.PluginsPaths: <data>/run/plugin-api.sock, or /tmp/maktaba-<hash>/ when that
// is too long for a unix socket).
func pluginAPISocket(dataDir string) string {
	p := filepath.Join(dataDir, "run", "plugin-api.sock")
	if len(filepath.Join(dataDir, "run", "plugins", "plugind.sock")) > 100 || len(p) > 100 {
		sum := sha256.Sum256([]byte(filepath.Clean(dataDir)))
		p = filepath.Join("/tmp", "maktaba-"+hex.EncodeToString(sum[:6]), "plugin-api.sock")
	}
	return p
}

type pluginMachine struct {
	api, socket, tokenFile string
}

func (m pluginMachine) mint(t *testing.T, jobID int64) (int, map[string]any) {
	t.Helper()
	tok, err := os.ReadFile(m.tokenFile)
	if err != nil {
		t.Fatalf("the local token: %v", err)
	}
	b, _ := json.Marshal(map[string]int64{"job_id": jobID})
	req, _ := http.NewRequest(http.MethodPost, m.api+"/internal/plugins/invocations", bytes.NewReader(b))
	req.Header.Set("X-Maktaba-Local-Token", strings.TrimSpace(string(tok)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (m pluginMachine) call(t *testing.T, token, route string, body any) (int, []byte) {
	t.Helper()
	c := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", m.socket)
	}}}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, "http://plugin-api/plugin-api/v1/"+route, bytes.NewReader(b))
	req.Header.Set("Authorization", "Plugin "+token)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", route, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// pluginsAcrossMachines is the subtest (called from TestTwoMachinesOnePC while both
// machines run).
func pluginsAcrossMachines(t *testing.T, db *sql.DB, home, node pluginMachine, video, library string) {
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	const a, b = "dev.example.finder", "dev.example.marker"
	pkgA := filepath.Join(t.TempDir(), a, "1.0.0_aaaaaaaaaaaa")
	_ = os.MkdirAll(filepath.Join(pkgA, "events"), 0o755)
	_ = os.WriteFile(filepath.Join(pkgA, "events", "found.schema.json"), []byte(`{"type":"object"}`), 0o644)
	stageA, stageB := pl.QueueName(a, "find"), pl.QueueName(b, "mark")
	// the two plugins as an install leaves them: A on the home, B anywhere, subscribed
	exec(`INSERT INTO worker_pools (pool, label, scope, size, default_size) VALUES ('plugins','Plugins','global',1,1) ON CONFLICT DO NOTHING`)
	for _, s := range [][2]string{{stageA, a}, {stageB, b}} {
		exec(`INSERT INTO job_stages (stage, label, subject_kinds, pool, lane, extension) VALUES ($1, $1, '{video}', 'plugins', 'media', $2)`,
			s[0], "plugin:"+s[1])
	}
	exec(`INSERT INTO plugin_installs (plugin_id, version, digest, path, manifest) VALUES ($1, '1.0.0', 'd', $2, $3)`, a, pkgA,
		`{"id":"`+a+`","version":"1.0.0","resources":{"timeout_s":60,"data_mb":1},
		"stage":[{"name":"find","subject":"video","emits":["found.it"],"may_request":[]}],
		"event":[{"name":"found.it","subject":"video","payload":"events/found.schema.json"}]}`)
	exec(`INSERT INTO plugin_installs (plugin_id, version, digest, path, manifest) VALUES ($1, '1.0.0', 'd', '/home/b', $2)`, b,
		`{"id":"`+b+`","version":"1.0.0","placement":"any","resources":{"timeout_s":60,"data_mb":1},
		"stage":[{"name":"mark","subject":"video","may_request":[],"trigger":[{"event":"plugin:`+a+`/found.it"}]}],
		"depends":[{"plugin":"`+a+`","version":">=1.0.0"}]}`)
	exec(`INSERT INTO plugin_grants (plugin_id, capabilities, libraries) VALUES ($1, '{library.read}', CAST($2 AS uuid[])),
		($3, $4, CAST($2 AS uuid[]))`, a, pq.Array([]string{library}), b, pq.Array([]string{"library.read", "tags.write", "events.subscribe:" + a}))
	exec(`INSERT INTO components (id, want, updated_by) VALUES ($1, 'enabled', 'test'), ($2, 'enabled', 'test')`, "plugin."+a, "plugin."+b)

	// A runs on the home and emits; the home names B's stage
	var jobA int64
	if err := db.QueryRowContext(ctx, `INSERT INTO processing_jobs (stage, state, subject_kind, subject_id, video_id)
		VALUES ($1, 'running', 'video', $2, $2) RETURNING id`, stageA, video).Scan(&jobA); err != nil {
		t.Fatal(err)
	}
	code, minted := home.mint(t, jobA)
	if code != 200 {
		t.Fatalf("A's token on the home: %d %v", code, minted)
	}
	code, out := home.call(t, minted["token"].(string), "events/emit",
		map[string]any{"name": "found.it", "subject": map[string]string{"kind": "video", "id": video}, "payload": `{"n":1}`})
	if code != 200 || !strings.Contains(string(out), stageB) {
		t.Fatalf("A's event: %d %s", code, out)
	}
	// the home's pipeline enqueues B's job with the lineage extended (54.40); B is placed
	// any, so the machine claims it
	lineage, _ := json.Marshal(minted["lineage"])
	var l pl.Lineage
	_ = json.Unmarshal(lineage, &l)
	next, ok := l.Next(stageB, "video:"+video)
	if !ok {
		t.Fatal("no lineage for B")
	}
	payload, _ := json.Marshal(map[string]any{"event": "found.it", "from": a, "payload": map[string]int{"n": 1}, pl.LineageKey: next})
	var jobB int64
	if err := db.QueryRowContext(ctx, `INSERT INTO processing_jobs (stage, state, subject_kind, subject_id, video_id, payload)
		VALUES ($1, 'running', 'video', $2, $2, $3) RETURNING id`, stageB, video, string(payload)).Scan(&jobB); err != nil {
		t.Fatal(err)
	}

	// B runs on the machine: its token is the home's, its read the machine's, its write the home's
	var tokB string
	waitFor(t, "the machine's plugin API", 60*time.Second, func() bool {
		if _, err := os.Stat(node.socket); err != nil {
			return false
		}
		code, m := node.mint(t, jobB)
		if code == 200 {
			tokB, _ = m["token"].(string)
		}
		return code == 200
	})
	if code, out := node.call(t, tokB, "library/video", map[string]string{"id": video}); code != 200 {
		t.Fatalf("B's read on the machine: %d %s", code, out)
	}
	if code, out := node.call(t, tokB, "write/add-tags", map[string]any{"video": video, "tags": []string{"Found"}}); code != 200 {
		t.Fatalf("B's write through the home: %d %s", code, out)
	}
	var writes, hops int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM plugin_writes WHERE plugin_id = $1`, b).Scan(&writes)
	_ = db.QueryRowContext(ctx, `SELECT (lineage->>'hops')::int FROM plugin_write_lineage WHERE subject_id = $1`, video).Scan(&hops)
	if writes == 0 || hops != 2 {
		t.Fatalf("the home applied B's write: writes %d, lineage hops %d", writes, hops)
	}

	// B off on the home: the machine refuses its next call, and mints no more
	exec(`UPDATE components SET want = 'installed' WHERE id = $1`, "plugin."+b)
	start := time.Now()
	waitFor(t, "B off on the machine", 5*time.Second, func() bool {
		code, _ := node.call(t, tokB, "library/video", map[string]string{"id": video})
		return code == 401
	})
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("off reached the machine in %s, more than 2 s (N9)", took)
	}
	if code, _ := node.mint(t, jobB); code == 200 {
		t.Fatal("a token for an off plugin")
	}
}
