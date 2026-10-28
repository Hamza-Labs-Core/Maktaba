package tvremote_test

// Epic 33 stories 33.5 and 33.6 end to end on one machine, with real processes
// and real ffmpeg: the home's front door (maktaba-streaming serve, built with the
// tvstaging tag), the live slot (blue), the hot standby (green) and the staging
// slot (amber, started on demand by a stand-in for the supervisor's
// /slots/{name}/start, with the release binding) against a scratch Postgres, the
// change controller (in this process, talking to the front door over its signed
// internal routes), an HLS viewer on live and the preview through the front door.
//
//   - a staged change starts amber on its release; amber warms the watched
//     channel, the probe channel and the changed channel (rolling); every stream
//     check runs and the change becomes ready after the soak, with the report on
//     the change (verify_report.checks);
//   - the preview serves the staging slot's HLS for the token only, the staged
//     lineup and guide; live viewers stay on live;
//   - cancelling the change stops amber;
//   - broken releases fail with the right reason: a binary with other PIDs
//     (ts.conformity, splice.align), a binary whose timestamps are 700 ms off the
//     wall clock (splice.align), a release missing a watched channel
//     (lineup.sanity).
//
// Runs only with MAKTABA_TEST_PG_URL and ffmpeg on PATH; never in -short.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	tvchangeapi "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tvchange"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
)

// fakeSupervisor is the supervisor's /slots/{name}/start|stop for the on-demand
// instance: it runs the slot binary of the release's version (as the supervisor
// runs bin/releases/<version>/) with MAKTABA_TV_RELEASE.
type fakeSupervisor struct {
	t      *testing.T
	bins   map[string]string // version -> binary
	env    func(name string) []string
	mu     sync.Mutex
	procs  map[string]*e2eProc
	starts []string
	stops  []string
}

func (f *fakeSupervisor) handler(managed map[string]bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/slots/"), "/"), "/")
		if r.Method != http.MethodPost || len(parts) != 2 {
			http.Error(w, "bad", 400)
			return
		}
		name, verb := parts[0], parts[1]
		if !managed[name] {
			_, _ = io.WriteString(w, "{}") // blue / green: the test runs them
			return
		}
		var b struct {
			ReleaseID, Version string
		}
		if verb == "start" && r.ContentLength > 0 {
			var raw map[string]string
			_ = json.NewDecoder(r.Body).Decode(&raw)
			b.ReleaseID, b.Version = raw["release_id"], raw["version"]
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if p := f.procs[name]; p != nil {
			p.kill()
			delete(f.procs, name)
		}
		if verb == "stop" {
			f.stops = append(f.stops, name)
			_, _ = io.WriteString(w, "{}")
			return
		}
		bin := f.bins[b.Version]
		if bin == "" {
			http.Error(w, "tv_slot_refused: no binary for version "+b.Version, http.StatusConflict)
			return
		}
		env := append(f.env(name), "MAKTABA_TV_RELEASE="+b.ReleaseID)
		f.procs[name] = startSlotProc(f.t, bin, "slot-"+name+"-"+b.Version, []string{"slot", "--slot", name}, env)
		f.starts = append(f.starts, name+"@"+b.Version)
		_, _ = io.WriteString(w, "{}")
	})
}

func (f *fakeSupervisor) running(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.procs[name]
	return p != nil && p.cmd.ProcessState == nil
}

func buildStreaming(t *testing.T, dir, out string, ldflags string) {
	t.Helper()
	tags := "tvstaging"
	if strings.Contains(ldflags, "BuildDefect=") {
		tags += ",tvdefect" // a deliberately broken release: only a tvdefect build applies it
	}
	args := []string{"build", "-tags", tags, "-o", out}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	cmd := exec.Command("go", append(args, ".")...)
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", out, err, b)
	}
}

// grid writes 30-minute blocks (no video: the slot airs a slate, keeping the block) of a channel's generation from 2 h ago to
// 26 h ahead; title(i) names block i (counted from now's block).
func grid(t *testing.T, db *sql.DB, ch string, gen int64, now time.Time, title func(i int) string) {
	t.Helper()
	start := now.Truncate(30 * time.Minute).Add(-2 * time.Hour)
	for i := -4; i < 52; i++ {
		s := start.Add(time.Duration(i+4) * 30 * time.Minute)
		snap, _ := json.Marshal(map[string]any{"title": title(i)})
		if _, err := db.Exec(`INSERT INTO channel_programs (channel_id, seq, kind, start_at, end_at, source_duration,
			title_snapshot, sched_gen) VALUES ($1, $2, 'program', $3, $4, 1800000, $5, $6)`,
			ch, int64(i+100), s, s.Add(30*time.Minute), string(snap), gen); err != nil {
			t.Fatal(err)
		}
	}
}

type changeRow struct {
	state, reason string
	report        map[string]any
}

func readChange(t *testing.T, db *sql.DB, id string) changeRow {
	t.Helper()
	var c changeRow
	var rep []byte
	var reason sql.NullString
	if err := db.QueryRow(`SELECT state, end_reason, verify_report FROM tv_changes WHERE id = $1`, id).
		Scan(&c.state, &reason, &rep); err != nil {
		t.Fatal(err)
	}
	c.reason = reason.String
	_ = json.Unmarshal(rep, &c.report)
	if c.state == "draft" {
		// a failed verification goes back to draft (story 33.12): its reason is the
		// fail event's
		var why sql.NullString
		_ = db.QueryRow(`SELECT detail->>'reason' FROM tv_change_events WHERE change_id = $1 AND action = 'fail'
			ORDER BY at DESC LIMIT 1`, id).Scan(&why)
		c.reason = why.String
	}
	return c
}

func checksOf(c changeRow) []map[string]any {
	xs, _ := c.report["checks"].([]any)
	out := []map[string]any{}
	for _, x := range xs {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func TestStagingVerifyEndToEnd(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_PG_URL") == "" || testing.Short() {
		t.Skip("MAKTABA_TEST_PG_URL unset (or -short): skipping the TV staging end-to-end test")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	db, dsn := clustertest.ScratchDB(t)

	_, file, _, _ := runtime.Caller(0)
	streamingDir := filepath.Join(filepath.Dir(file), "..", "..", "..", "streaming")
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	tmp := t.TempDir()
	const pkg = "github.com/Hamza-Labs-Core/Maktaba/streaming/internal/"
	bins := map[string]string{
		"e33-good":     filepath.Join(tmp, "good", "maktaba-streaming"+exe),
		"e33-bad-pids": filepath.Join(tmp, "pids", "maktaba-streaming"+exe),
		"e33-bad-pts":  filepath.Join(tmp, "pts", "maktaba-streaming"+exe),
	}
	t0 := time.Now()
	buildStreaming(t, streamingDir, bins["e33-good"], "-X "+pkg+"version.Version=e33-good")
	buildStreaming(t, streamingDir, bins["e33-bad-pids"], "-X "+pkg+"version.Version=e33-bad-pids -X "+pkg+"channel.BuildDefect=pids")
	buildStreaming(t, streamingDir, bins["e33-bad-pts"], "-X "+pkg+"version.Version=e33-bad-pts -X "+pkg+"channel.BuildDefect=pts:700")
	t.Logf("built the streaming binary and two broken releases in %v", time.Since(t0).Round(time.Second))
	bin := bins["e33-good"]

	now := time.Now().UTC()
	ch1, ch2 := uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES
		($1, 101, 'First', 'first', 'shuffle'), ($2, 102, 'Second', 'second', 'shuffle')`, ch1, ch2); err != nil {
		t.Fatal(err)
	}
	output := `{"encoder":"software","resolution":"854x480","fps":30,"video_kbps":2500,"audio_kbps":96,"segment_seconds":2,"warm_grace_seconds":120}`
	if _, err := db.Exec(`INSERT INTO app_settings (key, value, updated_at) VALUES
		('rules.tv_output', $1, now()),
		('rules.tv_staging', '{"enabled":true,"verify_soak_seconds":20,"verify_probe_seconds":8}', now())`, output); err != nil {
		t.Fatal(err)
	}
	// generation 0 is live; the releases' generations copy it up to the
	// boundary (the block on the air now and the next) and differ after
	same := func(name string) func(int) string {
		return func(i int) string { return fmt.Sprintf("%s %d", name, i) }
	}
	grid(t, db, ch1, 0, now, same("First"))
	grid(t, db, ch2, 0, now, same("Second"))

	fdPort, bluePort, greenPort, amberPort := e2eFreePort(t), e2eFreePort(t), e2eFreePort(t), e2eFreePort(t)
	cache := filepath.Join(tmp, "cache")
	noNode := filepath.Join(tmp, "no-node.json")
	fdBase := fmt.Sprintf("http://127.0.0.1:%d", fdPort)
	common := []string{"DATABASE_URL=" + dsn, "MAKTABA_NODE_CONFIG=" + noNode, "MAKTABA_FFMPEG_BIN=ffmpeg",
		"MAKTABA_FFPROBE_BIN=ffprobe", "MAKTABA_HEALTH_WARM=0s", "MAKTABA_JWKS_URL=", "MAKTABA_LOG_LEVEL=info",
		"MAKTABA_TV_HOST_ROLE=home", "MAKTABA_CLUSTER_ROLE=home", "MAKTABA_CACHE_ROOT=" + cache,
		"MAKTABA_PUBLIC_BASE_URL=" + fdBase}
	ports := map[string]int{"blue": bluePort, "green": greenPort, "amber": amberPort}
	slotEnv := func(name string) []string {
		return append(append([]string{}, common...), fmt.Sprintf("MAKTABA_HTTP_ADDR=127.0.0.1:%d", ports[name]),
			fmt.Sprintf("MAKTABA_ADMIN_ADDR=127.0.0.1:%d", e2eFreePort(t)))
	}
	sup := &fakeSupervisor{t: t, bins: bins, env: slotEnv, procs: map[string]*e2eProc{}}
	supSrv := httptest.NewServer(sup.handler(map[string]bool{"amber": true}))
	t.Cleanup(supSrv.Close)
	t.Cleanup(func() {
		sup.mu.Lock()
		defer sup.mu.Unlock()
		for _, p := range sup.procs {
			p.kill()
		}
	})

	startSlotProc(t, bin, "slot-blue", []string{"slot", "--slot", "blue"}, slotEnv("blue"))
	startSlotProc(t, bin, "slot-green", []string{"slot", "--slot", "green"}, slotEnv("green"))
	fd := startSlotProc(t, bin, "frontdoor", []string{"serve"}, append(append([]string{}, common...),
		fmt.Sprintf("MAKTABA_HTTP_ADDR=127.0.0.1:%d", fdPort),
		fmt.Sprintf("MAKTABA_ADMIN_ADDR=127.0.0.1:%d", e2eFreePort(t)),
		fmt.Sprintf("MAKTABA_STREAMING_GRPC_ADDR=127.0.0.1:%d", e2eFreePort(t)),
		"MAKTABA_SUPERVISOR_URL="+supSrv.URL,
		fmt.Sprintf("MAKTABA_TV_SLOTS=blue=127.0.0.1:%d,green=127.0.0.1:%d,amber=127.0.0.1:%d", bluePort, greenPort, amberPort)))
	blueBase := fmt.Sprintf("http://127.0.0.1:%d", bluePort)
	greenBase := fmt.Sprintf("http://127.0.0.1:%d", greenPort)
	amberBase := fmt.Sprintf("http://127.0.0.1:%d", amberPort)
	e2eWait(t, "both slots in their roles", 120*time.Second, func() bool {
		b, okB := getSlotStatus(blueBase)
		g, okG := getSlotStatus(greenBase)
		return okB && okG && b.Role == "live" && g.Role == "standby"
	})
	e2eWait(t, "the cluster stream key", 60*time.Second, func() bool {
		var n int
		return db.QueryRow(`SELECT count(*) FROM tv_cluster_secret`).Scan(&n) == nil && n > 0
	})

	// a viewer on 101 (live)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e2eWait(t, "the channel's playlist on live", 90*time.Second, func() bool {
		resp, err := http.Get(fdBase + "/stream/channel/" + ch1 + "/index.m3u8")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == 200
	})
	var liveBodies sync.Map
	go func() {
		for ctx.Err() == nil {
			if resp, err := http.Get(fdBase + "/stream/channel/" + ch1 + "/index.m3u8"); err == nil {
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				liveBodies.Store("last", string(b))
			}
			time.Sleep(time.Second)
		}
	}()
	e2eWait(t, "the standby to warm the watched channel", 60*time.Second, func() bool {
		g, ok := getSlotStatus(greenBase)
		return ok && contains(g.Stations, ch1)
	})

	// the change controller, as the API runs it
	ctl := tvchange.NewController(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctl.Enqueue = func(context.Context, string, map[string]any, string) error { return nil }
	ctl.Staging = &tvchange.HTTPStaging{DB: db, Base: fdBase}
	ctl.Interval = time.Second
	go ctl.Run(ctx)

	var gen int64
	release := func(version string, chans map[string]map[string]any, sched map[string]bool) string {
		gen++
		var id string
		settings := `{"tv_output":` + output + `}`
		if err := db.QueryRow(`INSERT INTO tv_releases (digest, kinds, sched_gen, streaming_version, settings)
			VALUES ($1, '["lineup","schedule"]', $2, $3, $4) RETURNING id`, uuid.NewString(), gen, version, settings).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for cid, def := range chans {
			b, _ := json.Marshal(def)
			if _, err := db.Exec(`INSERT INTO tv_release_channels (release_id, channel_id, def, sched_changed) VALUES ($1, $2, $3, $4)`,
				id, cid, string(b), sched[cid]); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	def := func(num int, name, slug string) map[string]any {
		return map[string]any{"number": num, "name": name, "slug": slug, "enabled": true, "mode": "shuffle",
			"mode_config": map[string]any{}, "logo_path": "/logos/" + slug + ".png"}
	}
	diff := `{"lineup":{"added":[],"removed":[],"renumbered":[],"renamed":[{"id":"` + ch2 +
		`","from":"Second","to":"Second (new)"}],"changed":[]},"schedules":[{"id":"` + ch2 + `","reason":"regenerated"}]}`
	stage := func(rel, title string) string {
		var id string
		if err := db.QueryRow(`INSERT INTO tv_changes (release_id, state, kinds, title, source, diff, staged_at)
			VALUES ($1, 'staged', '["lineup","schedule"]', $2, '{"kind":"manual"}', $3, now()) RETURNING id`,
			rel, title, diff).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	waitState := func(id string, want []string, d time.Duration) changeRow {
		t.Helper()
		var c changeRow
		e2eWait(t, "change "+id[:8]+" to be "+strings.Join(want, "/"), d, func() bool {
			c = readChange(t, db, id)
			return contains(want, c.state)
		})
		return c
	}
	good := func(gen int64, chans ...string) map[string]map[string]any {
		m := map[string]map[string]any{}
		for _, c := range chans {
			if c == ch1 {
				m[c] = def(101, "First", "first")
				grid(t, db, ch1, gen, now, same("First"))
			} else {
				m[c] = def(102, "Second (new)", "second")
				grid(t, db, ch2, gen, now, func(i int) string {
					if i <= 1 {
						return fmt.Sprintf("Second %d", i) // the block on air and the next are kept
					}
					return fmt.Sprintf("New Second %d", i)
				})
			}
		}
		return m
	}

	// ---- 1. a good change: staged → verifying → ready -------------------
	rel1 := release("e33-good", good(gen+1, ch1, ch2), map[string]bool{ch2: true})
	c1 := stage(rel1, "rename 102")
	tStage := time.Now()
	waitState(c1, []string{"verifying", "draft"}, 90*time.Second)
	verifyingAfter := time.Since(tStage)
	if c := readChange(t, db, c1); c.state == "draft" {
		t.Fatalf("the good change failed at the slot start: %v", c.report)
	}
	st, _ := getSlotStatus(amberBase)
	var role, boundRel string
	_ = db.QueryRow(`SELECT role, COALESCE(release_id::text, '') FROM tv_slots WHERE slot = 'amber'`).Scan(&role, &boundRel)
	if st.Role != "staging" || role != "staging" || boundRel != rel1 {
		t.Fatalf("amber: status %+v, tv_slots role %q release %q (want staging on %s)", st, role, boundRel, rel1)
	}
	t.Logf("33.5: staged → verifying in %v (amber started on release %s, version e33-good)",
		verifyingAfter.Round(100*time.Millisecond), rel1[:8])
	c := waitState(c1, []string{"ready", "draft"}, 4*time.Minute)
	readyAfter := time.Since(tStage)
	checks := checksOf(c)
	if c.state != "ready" {
		var bad []string
		for _, k := range checks {
			if k["result"] == "fail" {
				bad = append(bad, fmt.Sprint(k["check"], " ", k["channel_id"], ": ", k["value"]))
			}
		}
		t.Fatalf("the good change %s (%s): %v %v", c.state, c.reason, bad, c.report["reasons"])
	}
	names := map[string]int{}
	for _, k := range checks {
		names[k["check"].(string)]++
		if k["result"] == "fail" {
			t.Errorf("a failed check on a ready change: %v", k)
		}
	}
	for _, want := range []string{"slot.health", "channel.first_segment", "channel.cadence", "channel.wallclock",
		"channel.position", "channel.block", "ts.conformity", "hls.conformity", "splice.align", "splice.compat",
		"cloudflare.ingest", "guide.vs_stream", "encoders", "encoders.budget", "guide.coverage", "schedule.boundary"} {
		if names[want] == 0 {
			t.Errorf("no %s check in the report", want)
		}
	}
	for _, k := range checks {
		if k["check"] == "slot.health" && k["result"] != "pass" {
			t.Errorf("slot.health: %v", k)
		}
	}
	perCh := map[string]bool{}
	for _, k := range checks {
		if k["check"] == "ts.conformity" {
			perCh[fmt.Sprint(k["channel_id"])] = true
			t.Logf("  ts.conformity %s: %v", fmt.Sprint(k["channel_id"])[:8], k["value"])
		}
		if k["check"] == "splice.align" || k["check"] == "channel.first_segment" || k["check"] == "encoders.budget" ||
			k["check"] == "channel.wallclock" || k["check"] == "channel.position" {
			t.Logf("  %s %v: %v", k["check"], k["channel_id"], k["value"])
		}
	}
	if !perCh[ch1] || !perCh[ch2] {
		t.Errorf("not both channels measured (watched 101 and the changed 102 by its rolling probe): %v", perCh)
	}
	sum, _ := c.report["summary"].(map[string]any)
	t.Logf("33.5: ready %v after staging (soak 20 s): %v checks, summary %v", readyAfter.Round(100*time.Millisecond),
		len(checks), sum)

	// ---- 2. the preview (33.6) ----------------------------------------------
	t.Setenv("MAKTABA_PUBLIC_BASE_URL", fdBase) // the API's streaming origin (the M3U for VLC)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &principal.Principal{UserID: uuid.NewString(), IsAdmin: req.Header.Get("X-Test-Admin") == "1"}
			next.ServeHTTP(w, req.WithContext(principal.WithPrincipal(req.Context(), p)))
		})
	})
	tvchangeapi.New(db, ctl).Mount(r)
	api := httptest.NewServer(r)
	t.Cleanup(api.Close)
	post := func(path string, admin bool) (int, []byte) {
		req, _ := http.NewRequest(http.MethodPost, api.URL+path, nil)
		if admin {
			req.Header.Set("X-Test-Admin", "1")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	if code, _ := post("/api/tv/changes/"+c1+"/preview", false); code != http.StatusForbidden {
		t.Errorf("preview for a non-admin: %d", code)
	}
	code, body := post("/api/tv/changes/"+c1+"/preview", true)
	if code != 200 {
		t.Fatalf("preview: %d %s", code, body)
	}
	var pv tvchangeapi.PreviewReply
	_ = json.Unmarshal(body, &pv)
	var pv2 *tvchangeapi.PreviewChannel
	for i := range pv.Channels {
		if pv.Channels[i].ID == ch2 {
			pv2 = &pv.Channels[i]
		}
	}
	if pv2 == nil || pv2.Name != "Second (new)" || !pv2.Changed || !strings.Contains(pv2.URL, "?pt=") {
		t.Fatalf("preview channels: %+v", pv.Channels)
	}
	tPrev := time.Now()
	var plBody string
	e2eWait(t, "the staging playlist of 102 through the front door", 60*time.Second, func() bool {
		resp, err := http.Get(fdBase + pv2.URL)
		if err != nil {
			return false
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		plBody = string(b)
		return resp.StatusCode == 200 && strings.Contains(plBody, "s/amber/seg-")
	})
	t.Logf("33.6: preview playlist of 102 (staging) in %v", time.Since(tPrev).Round(100*time.Millisecond))
	segURI := ""
	for _, l := range strings.Split(plBody, "\n") {
		if strings.HasPrefix(l, "s/amber/") {
			segURI = l
		}
	}
	resp, err := http.Get(fdBase + "/stream/staging/channel/" + ch2 + "/" + segURI)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("staging segment: %v %v", resp, err)
	}
	segBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if len(segBytes) < 188*10 || segBytes[0] != 0x47 {
		t.Fatalf("staging segment is not TS (%d bytes)", len(segBytes))
	}
	if resp, err := http.Get(fdBase + strings.Split(pv2.URL, "?")[0]); err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("preview without the token: %d", resp.StatusCode)
		}
	}
	if v, ok := liveBodies.Load("last"); !ok || strings.Contains(v.(string), "s/amber/") ||
		!strings.Contains(v.(string), "s/blue/") {
		t.Errorf("the live viewer's playlist is not live's: %v", v)
	}
	getText := func(u string) (int, string) {
		resp, err := http.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, m3u := getText(pv.M3U); code != 200 || !strings.Contains(m3u, "Second (new)") ||
		!strings.Contains(m3u, "/stream/staging/channel/"+ch2+"/index.m3u8?pt=") {
		t.Errorf("staging M3U %d: %s", code, m3u)
	}
	if code, x := getText(pv.XMLTV); code != 200 || !strings.Contains(x, "Second (new)") ||
		!strings.Contains(x, "New Second 3") || strings.Contains(x, "<title lang=\"en\">Second 3</title>") {
		t.Errorf("staging XMLTV %d (want the staged generation's guide)", code)
	}
	if _, x := getText(fdBase + "/xmltv.xml"); !strings.Contains(x, "Second 3") || strings.Contains(x, "New Second") {
		t.Errorf("the live guide changed with a staged change")
	}
	if code, d := getText(fdBase + "/discover.json"); code == 200 && strings.Contains(d, "staging") {
		t.Errorf("a staging HDHomeRun device: %s", d)
	}
	t.Logf("33.6: staging M3U and XMLTV (generation %d) served for the token; the live guide unchanged", gen)

	// ---- 3. cancel: the staging slot stops ---------------------------------
	if code, b := post("/api/tv/changes/"+c1+"/cancel", true); code != 200 {
		t.Fatalf("cancel: %d %s", code, b)
	}
	tCancel := time.Now()
	e2eWait(t, "amber to stop after the cancel", 30*time.Second, func() bool {
		_, ok := getSlotStatus(amberBase)
		return !ok && !sup.running("amber")
	})
	_ = db.QueryRow(`SELECT role FROM tv_slots WHERE slot = 'amber'`).Scan(&role)
	t.Logf("33.5: cancelled → staging slot stopped in %v (tv_slots amber %s)", time.Since(tCancel).Round(100*time.Millisecond), role)
	if role != "idle" {
		t.Errorf("amber's role after the cancel: %s", role)
	}

	// ---- 4. broken releases fail with the right reason --------------------
	type badCase struct {
		name, version string
		chans         []string
		want          []string // check names that must fail
		wantValue     string
	}
	for _, bc := range []badCase{
		{"wrong PIDs", "e33-bad-pids", []string{ch1, ch2}, []string{"ts.conformity", "splice.align"}, "PIDs"},
		{"PTS 700 ms off", "e33-bad-pts", []string{ch1, ch2}, []string{"splice.align"}, "ms off live's"},
		{"a watched channel missing", "e33-good", []string{ch2}, []string{"lineup.sanity"}, "missing from the release"},
	} {
		rel := release(bc.version, good(gen+1, bc.chans...), map[string]bool{ch2: true})
		id := stage(rel, bc.name)
		t1 := time.Now()
		c := waitState(id, []string{"draft", "ready"}, 4*time.Minute)
		if c.state != "draft" {
			t.Errorf("%s: the change is %s, want failed back to draft", bc.name, c.state)
			continue
		}
		failed := map[string]string{}
		for _, k := range checksOf(c) {
			if k["result"] == "fail" {
				failed[k["check"].(string)] = fmt.Sprint(k["value"])
			}
		}
		for _, w := range bc.want {
			if _, ok := failed[w]; !ok {
				t.Errorf("%s: %s did not fail (failed: %v)", bc.name, w, failed)
			}
		}
		if !strings.Contains(fmt.Sprint(c.report["reasons"]), bc.wantValue) {
			t.Errorf("%s: reasons %v do not say %q", bc.name, c.report["reasons"], bc.wantValue)
		}
		if c.reason != "verification_failed" {
			t.Errorf("%s: end_reason %q", bc.name, c.reason)
		}
		t.Logf("33.5: %s → failed in %v: %v", bc.name, time.Since(t1).Round(100*time.Millisecond), c.report["reasons"])
		e2eWait(t, "amber to stop after the failure", 30*time.Second, func() bool { return !sup.running("amber") })
		// the failed change is the open draft: the next one would join it (story 33.12 AC3)
		if code, b := post("/api/tv/changes/"+id+"/cancel", true); code != 200 {
			t.Fatalf("%s: cancel the failed draft: %d %s", bc.name, code, b)
		}
	}

	// staging never pushed and never took the live slot's place
	b, _ := getSlotStatus(blueBase)
	if b.Role != "live" {
		t.Errorf("blue is %s after the staging runs", b.Role)
	}
	fb, _ := os.ReadFile(fd.log)
	if strings.Contains(string(fb), "tv_slot_failover") {
		t.Error("a failover happened during staging")
	}
	sup.mu.Lock()
	t.Logf("supervisor: starts %v, stops %v", sup.starts, sup.stops)
	sup.mu.Unlock()
}
