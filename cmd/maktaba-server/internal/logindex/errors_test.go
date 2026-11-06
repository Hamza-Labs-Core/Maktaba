package logindex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func errLine(ts, msg, code, emsg, version, user, rid string) string {
	m := map[string]any{"level": "error", "msg": msg, "ts": ts, "version": version,
		"err": map[string]any{"code": code, "msg": emsg, "stack": []any{
			map[string]any{"fn": "runtime.goexit", "own": false},
			map[string]any{"fn": "handlers/videos.(*H).Get", "own": true}}}}
	if user != "" {
		m["user_h"] = user
	}
	if rid != "" {
		m["request_id"] = rid
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// T07: lines that differ only in ids, numbers and paths are one group; another code is
// another group; counts, users, samples and the day's sparkline add up; warn lines are no group.
func TestErrorGroups(t *testing.T) {
	root, now := t.TempDir(), t0
	k := keeperAt(t, root, &now)
	write(t, k, "api", "",
		errLine("2026-11-06T10:00:00Z", "video_load_failed", "db.timeout", "query 12 took 5003ms on /data/a/1.mkv", "0.3.80", "u1", "r1"),
		errLine("2026-11-06T10:00:01Z", "video_load_failed", "db.timeout", "query 98 took 5121ms on /data/b/2.mkv", "0.3.80", "u2", "r2"),
		errLine("2026-11-06T10:00:02Z", "video_load_failed", "db.timeout", "query 7 took 5000ms on /data/c/3.mkv", "0.3.80", "u1", "r3"),
		errLine("2026-11-06T10:00:03Z", "video_load_failed", "fs.missing", "no such file", "0.3.80", "", ""),
		`{"level":"warn","msg":"slow","ts":"2026-11-06T10:00:04Z"}`)
	ix := open(t, root, &now)
	if _, err := ix.Ingest(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	gs, err := ix.Groups(ctx, GroupQuery{Sort: "count"})
	if err != nil || len(gs) != 2 {
		t.Fatalf("groups: %+v %v", gs, err)
	}
	g := gs[0]
	if g.Count != 3 || g.Users != 2 || g.Count24h != 3 || g.Code != "db.timeout" || g.Status != "new" ||
		g.RequestID != "r3" || g.Days[len(g.Days)-1] != 3 {
		t.Fatalf("the db.timeout group: %+v", g)
	}
	if strings.Contains(g.Title, "/data/a") {
		t.Errorf("the title has no paths: %q", g.Title)
	}
	s, _ := ix.Samples(ctx, g.FP)
	if len(s.Lines) != 3 || !strings.Contains(string(s.Lines[0]), `"r3"`) {
		t.Errorf("samples newest first: %d", len(s.Lines))
	}
	var n int
	_ = ix.db.QueryRow(`SELECT COUNT(*) FROM lines WHERE error_fp = ?`, g.FP).Scan(&n)
	if n != 3 {
		t.Errorf("lines carry their group: %d", n)
	}
}

// T08: resolved in 0.3.80, seen again on 0.3.81: new again and "came back"; seen again on
// 0.3.80 itself (an old node still running it) stays resolved. A mute for a time ends.
func TestResolvedComesBack(t *testing.T) {
	root, now := t.TempDir(), t0
	k := keeperAt(t, root, &now)
	write(t, k, "api", "", errLine("2026-11-06T10:00:00Z", "boom", "x.y", "bad", "0.3.80", "", ""))
	ix := open(t, root, &now)
	ctx := context.Background()
	_, _ = ix.Ingest(ctx)
	gs, _ := ix.Groups(ctx, GroupQuery{})
	fp := gs[0].FP
	if err := ix.SetStatus(ctx, fp, "resolved", "0.3.80", time.Time{}); err != nil {
		t.Fatal(err)
	}
	write(t, k, "api", "", errLine("2026-11-06T10:01:00Z", "boom", "x.y", "bad", "0.3.80", "", ""))
	_, _ = ix.Ingest(ctx)
	if g, _ := ix.Group(ctx, fp, 0); g.Status != "resolved" {
		t.Fatalf("the same version: %s", g.Status)
	}
	write(t, k, "api", "", errLine("2026-11-06T10:02:00Z", "boom", "x.y", "bad", "0.3.81", "", ""))
	_, _ = ix.Ingest(ctx)
	if g, _ := ix.Group(ctx, fp, 0); g.Status != "new" || g.CameBackIn != "0.3.81" {
		t.Fatalf("came back: %+v", g)
	}
	if err := ix.SetStatus(ctx, fp, "muted", "", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if gs, _ := ix.Groups(ctx, GroupQuery{}); len(gs) != 0 {
		t.Errorf("a muted group is out of the default list")
	}
	write(t, k, "api", "", errLine("2026-11-06T11:30:00Z", "boom", "x.y", "bad", "0.3.81", "", ""))
	_, _ = ix.Ingest(ctx)
	if g, _ := ix.Group(ctx, fp, 0); g.Status != "seen" {
		t.Errorf("the mute ended: %s", g.Status)
	}
	if err := ix.SetStatus(ctx, fp, "deleted", "", time.Time{}); err != ErrStatus {
		t.Errorf("an unknown status: %v", err)
	}
	if err := ix.SetStatus(ctx, "nope", "seen", "", time.Time{}); err == nil {
		t.Errorf("an unknown group")
	}
	for _, c := range []struct {
		a, b string
		less bool
	}{{"0.3.80", "0.3.81", true}, {"0.3.81", "0.3.80", false}, {"v0.3.9", "0.3.10", true}, {"", "0.1.0", true},
		{"0.3.80", "0.3.80-rc1", false}} {
		if versionLess(c.a, c.b) != c.less {
			t.Errorf("versionLess(%q, %q)", c.a, c.b)
		}
	}
}

// T09: notices: a new group with 3 lines in 10 minutes, once a day; one line is not enough;
// at most the day's limit, the rest counted.
func TestNotices(t *testing.T) {
	root, now := t.TempDir(), t0
	k := keeperAt(t, root, &now)
	var lines []string
	for _, code := range []string{"a.one", "b.two", "c.three"} {
		for i := 0; i < 3; i++ {
			lines = append(lines, errLine("2026-11-06T10:1"+string(rune('0'+i))+":00Z", "fail", code, "x", "0.3.80", "", ""))
		}
	}
	lines = append(lines, errLine("2026-11-06T10:14:00Z", "fail", "d.once", "x", "0.3.80", "", ""))
	write(t, k, "api", "", lines...)
	ix := open(t, root, &now)
	ctx := context.Background()
	_, _ = ix.Ingest(ctx)
	ns, more, err := ix.Notices(ctx, 2)
	if err != nil || len(ns) != 2 || more != 1 {
		t.Fatalf("notices: %+v more %d %v", ns, more, err)
	}
	ns, more, _ = ix.Notices(ctx, 2)
	if len(ns) != 0 || more != 1 {
		t.Fatalf("the day's limit is spent: %+v %d", ns, more)
	}
	ns, _, _ = ix.Notices(ctx, 10)
	if len(ns) != 1 || ns[0].Title == "" {
		t.Fatalf("the third, and never the one-line group: %+v", ns)
	}
	if ns, _, _ = ix.Notices(ctx, 10); len(ns) != 0 {
		t.Fatalf("once a day: %+v", ns)
	}
}

// T10: groups not seen for 30 days go with their hours; the routes answer.
func TestErrorSweepAndRoutes(t *testing.T) {
	root, now := t.TempDir(), t0
	k := keeperAt(t, root, &now)
	write(t, k, "api", "", errLine("2026-11-06T10:00:00Z", "boom", "x.y", "bad", "0.3.80", "u1", ""))
	ix := open(t, root, &now)
	ctx := context.Background()
	_, _ = ix.Ingest(ctx)
	srv := httptest.NewServer(ix.Handler(nil))
	defer srv.Close()
	var list struct{ Groups []Group }
	r, _ := http.Get(srv.URL + "/errors?status=new&sort=users")
	_ = json.NewDecoder(r.Body).Decode(&list)
	_ = r.Body.Close()
	if len(list.Groups) != 1 {
		t.Fatalf("GET /errors: %+v", list)
	}
	fp := list.Groups[0].FP
	r, _ = http.Get(srv.URL + "/errors/" + fp)
	var one struct {
		Group   Group
		Samples Result
	}
	_ = json.NewDecoder(r.Body).Decode(&one)
	_ = r.Body.Close()
	if one.Group.FP != fp || len(one.Samples.Lines) != 1 {
		t.Fatalf("GET /errors/{fp}: %+v", one)
	}
	for body, code := range map[string]int{`{"status":"resolved","version":"0.3.80"}`: 204, `{"status":"gone"}`: 400, `{`: 400} {
		r, _ = http.Post(srv.URL+"/errors/"+fp+"/status", "application/json", strings.NewReader(body))
		if r.StatusCode != code {
			t.Errorf("status %s: %d, want %d", body, r.StatusCode, code)
		}
		_ = r.Body.Close()
	}
	if r, _ = http.Get(srv.URL + "/errors/nope"); r.StatusCode != 404 {
		t.Errorf("an unknown group: %d", r.StatusCode)
	}
	now = now.Add(31 * 24 * time.Hour)
	if _, err := ix.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = ix.db.QueryRow(`SELECT (SELECT COUNT(*) FROM error_groups) + (SELECT COUNT(*) FROM error_hours) + (SELECT COUNT(*) FROM error_users)`).Scan(&n)
	if n != 0 {
		t.Errorf("30 days after it was last seen the group goes: %d rows", n)
	}
}
