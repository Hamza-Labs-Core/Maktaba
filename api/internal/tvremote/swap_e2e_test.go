package tvremote_test

// Epic 33 stories 33.7, 33.8 and 33.9 end to end on one machine, with real
// processes and real ffmpeg: the home's front door and three TV slots
// (maktaba-streaming serve with MAKTABA_TV_SLOTS; maktaba-streaming slot --slot
// blue|green|amber) against a scratch Postgres, the API's change controller (in
// this process; the pipeline's swap and reverse commits are played by the test's
// queue), an HLS viewer, a TS (HDHomeRun-style) viewer, and a push through the
// front door's relay to a fake RTMPS ingest (rtmptest) whose fake Cloudflare
// (cftest) retransmits it to a fake simulcast output. It shows:
//
//   - a swap of a staged release switches the watched channel at its programme
//     boundary: the HLS playlist continues with one EXT-X-DISCONTINUITY at the
//     boundary, the TS connection stays open with continuous timestamps, the
//     unwatched channel switches at once, the change goes live, the old live
//     slot stays up as the previous slot;
//   - a warm rollback switches it back the same way;
//   - a crash of the new live slot inside the grace rolls back by itself to the
//     previous slot (reason auto:crash);
//   - a crash of the live slot while an unapproved change is staged fails over
//     to the hot standby running the live release, never to staging;
//   - through all of it the ingest sees one publisher session with monotonic
//     timestamps and no gap above one keyframe interval, and the simulcast
//     output's session never disconnects.
//
// Runs only with MAKTABA_TEST_PG_URL and ffmpeg on PATH; never in -short.

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tvcluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cftest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/rtmptest"
)

// hlsWatcher polls a stitched playlist like a player and remembers every
// segment it was offered (media sequence → entry), fetching each one.
type hlsWatcher struct {
	mu    sync.Mutex
	seen  map[int64]*hlsEntry
	order []int64
	err   string
}

func watchHLS(ctx context.Context, url string) *hlsWatcher {
	h := &hlsWatcher{seen: map[int64]*hlsEntry{}}
	base := strings.TrimSuffix(url, "index.m3u8")
	go func() {
		for ctx.Err() == nil {
			resp, err := http.Get(url)
			if err == nil {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode == 200 {
					h.add(string(body), base)
				}
			}
			time.Sleep(700 * time.Millisecond)
		}
	}()
	return h
}

func (h *hlsWatcher) add(body, base string) {
	var seq int64
	var cur hlsEntry
	idx := int64(0)
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "#EXT-X-MEDIA-SEQUENCE:"):
			seq, _ = strconv.ParseInt(strings.TrimPrefix(l, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case l == "#EXT-X-DISCONTINUITY":
			cur.disc = true
		case strings.HasPrefix(l, "#EXT-X-PROGRAM-DATE-TIME:"):
			cur.pdt, _ = time.Parse("2006-01-02T15:04:05.000Z", strings.TrimPrefix(l, "#EXT-X-PROGRAM-DATE-TIME:"))
		case strings.HasPrefix(l, "#EXTINF:"):
			cur.dur, _ = strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(l, "#EXTINF:"), ","), 64)
		case l != "" && !strings.HasPrefix(l, "#"):
			n := seq + idx
			idx++
			h.mu.Lock()
			if e, ok := h.seen[n]; ok {
				if e.uri != l {
					h.err = fmt.Sprintf("media sequence %d was %s, now %s", n, e.uri, l)
				}
			} else {
				e := cur
				e.seq, e.uri, e.at = n, l, time.Now()
				h.seen[n] = &e
				h.order = append(h.order, n)
				go func(e *hlsEntry) {
					r, err := http.Get(base + e.uri)
					if err == nil {
						_, _ = io.Copy(io.Discard, r.Body)
						r.Body.Close()
						h.mu.Lock()
						e.ok = r.StatusCode == 200
						h.mu.Unlock()
					}
				}(&e)
			}
			h.mu.Unlock()
			cur = hlsEntry{}
		}
	}
}

// entries is a copy of what was seen, in media-sequence order.
func (h *hlsWatcher) entries() []hlsEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]hlsEntry, 0, len(h.order))
	for _, n := range h.order {
		out = append(out, *h.seen[n])
	}
	return out
}

// lastSlot is the slot of the newest segment offered.
func (h *hlsWatcher) lastSlot() string {
	es := h.entries()
	if len(es) == 0 {
		return ""
	}
	parts := strings.Split(es[len(es)-1].uri, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// tsWatcher reads a TS connection and records its video PTS.
type tsWatcher struct {
	tsPTS
	done chan error
}

func watchTS(ctx context.Context, url string) *tsWatcher {
	w := &tsWatcher{done: make(chan error, 1)}
	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			w.done <- err
			return
		}
		defer resp.Body.Close()
		r := bufio.NewReaderSize(resp.Body, 64<<10)
		buf := make([]byte, 32<<10)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				w.feed(buf[:n])
			}
			if err != nil {
				w.done <- err
				return
			}
		}
	}()
	return w
}

// stats: frames, timestamps not increasing, largest step (90 kHz) since frame `from`.
func (w *tsWatcher) stats(from int) (frames, back int, maxStep int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if from < 1 {
		from = 1
	}
	for i := from; i < len(w.pts); i++ {
		d := w.pts[i] - w.pts[i-1]
		if d <= 0 {
			back++
		}
		if d > maxStep {
			maxStep = d
		}
	}
	return len(w.pts), back, maxStep
}

// publishTags is one key's audio/video tags at a fake RTMP server and its publishes.
func publishTags(s *rtmptest.Server, key string) (pubs []rtmptest.Publish, video []rtmptest.Tag) {
	for _, p := range s.Publishes() {
		if p.Key != key {
			continue
		}
		pubs = append(pubs, p)
		for _, tg := range p.Tags {
			if tg.Type == 9 && !tg.SeqHeader {
				video = append(video, tg)
			}
		}
	}
	return pubs, video
}

func maxGapMS(v []rtmptest.Tag) (gap uint32, back int) {
	for i := 1; i < len(v); i++ {
		if v[i].TS < v[i-1].TS {
			back++
			continue
		}
		if g := v[i].TS - v[i-1].TS; g > gap {
			gap = g
		}
	}
	return gap, back
}

func TestSwapRollbackEndToEnd(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_PG_URL") == "" || testing.Short() {
		t.Skip("MAKTABA_TEST_PG_URL unset (or -short): skipping the TV swap end-to-end test")
	}
	rtmptest.NeedFFmpeg(t, "ffmpeg", "ffprobe")
	t0 := time.Now()
	db, dsn := clustertest.ScratchDB(t)

	_, file, _, _ := runtime.Caller(0)
	streamingDir := filepath.Join(filepath.Dir(file), "..", "..", "..", "streaming")
	bin := filepath.Join(t.TempDir(), "maktaba-streaming")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-tags", "tvstaging", "-o", bin, ".")
	build.Dir = streamingDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build streaming: %v\n%s", err, out)
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	// two channels; slate blocks of 20 s on the wall-clock grid in generation 0
	// (release 1, live) and generation 1 (release 2, staged): the same block
	// boundaries in both (N+1 equals N before each boundary)
	const block = 20 * time.Second
	ch, ch2 := uuid.NewString(), uuid.NewString()
	mustExec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, 101, 'Swap', 'swap', 'shuffle'),
		($2, 102, 'Quiet', 'quiet', 'shuffle')`, ch, ch2)
	output := `{"encoder":"software","resolution":"640x360","fps":30,"video_kbps":600,"audio_kbps":64,"segment_seconds":2,"warm_grace_seconds":120}`
	mustExec(`INSERT INTO app_settings (key, value, updated_at) VALUES ('rules.tv_output', $1::jsonb, now())`, output)
	start := time.Now().Add(-2 * time.Minute).Truncate(block)
	for _, c := range []string{ch, ch2} {
		for gen := 0; gen <= 1; gen++ {
			for i := 0; i < 360; i++ {
				s := start.Add(time.Duration(i) * block)
				mustExec(`INSERT INTO channel_programs (channel_id, seq, kind, start_at, end_at, source_offset, source_duration, sched_gen)
					VALUES ($1, $2, 'slate', $3, $4, 0, $5, $6)`, c, i, s, s.Add(block), block.Milliseconds(), gen)
			}
		}
	}
	settings := `{"tv": {}, "tv_output": ` + output + `}`
	var r1, r2 string
	if err := db.QueryRow(`INSERT INTO tv_releases (digest, kinds, sched_gen, streaming_version, settings)
		VALUES ('e2e-r1', '["bootstrap"]', 0, 'dev', $1::jsonb) RETURNING id`, settings).Scan(&r1); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO tv_releases (digest, kinds, sched_gen, streaming_version, settings, base_release_id)
		VALUES ('e2e-r2', '["schedule"]', 1, 'dev', $1::jsonb, $2) RETURNING id`, settings, r1).Scan(&r2); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{r1, r2} {
		mustExec(`INSERT INTO tv_release_channels (release_id, channel_id, def, art, sched_changed) VALUES
			($1, $2, '{"name": "Swap", "number": 101, "enabled": true, "mode_config": {}}', '{}', true),
			($1, $3, '{"name": "Quiet", "number": 102, "enabled": true, "mode_config": {}}', '{}', false)`, rel, ch, ch2)
	}
	mustExec(`INSERT INTO tv_routing (scope, live_release_id, live_gen, epoch, switched_at) VALUES ('cluster', $1, 0, 1, now())`, r1)

	// the fake ingest, the fake Cloudflare with one live input and one simulcast
	// output retransmitted to a second fake RTMP server
	ing, err := rtmptest.Listen(true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ing.Close)
	dst, err := rtmptest.Listen(false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dst.Close)
	fcf := cftest.New(t)
	fcf.IngestRTMPS = ing.URL()
	cfc := fcf.Client(fcf.Account, fcf.Token)
	input, err := cfc.CreateLiveInput(context.Background(), cf.CreateLiveInput{Meta: map[string]any{"name": "swap-e2e"}})
	if err != nil || input.RTMPS == nil {
		t.Fatalf("live input: %v %+v", err, input)
	}
	inKey := input.RTMPS.StreamKey
	const simKey = "simulcast-key-0123"
	if _, err := cfc.CreateOutput(context.Background(), input.UID, dst.URL(), simKey, true); err != nil {
		t.Fatal(err)
	}
	ing.OnPublish = func(k string) {
		if k == inKey {
			fcf.GoLive(input.UID)
		}
	}
	ing.OnClose = func(k string) {
		if k == inKey {
			fcf.EndLive(input.UID)
		}
	}
	var fwdMu sync.Mutex
	forwards := 0
	fcf.Forward = func(u, key string) func() {
		fwdMu.Lock()
		forwards++
		fwdMu.Unlock()
		tap := ing.Tap(inKey)
		cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "flv", "-i", "pipe:0", "-c", "copy",
			"-f", "flv", strings.TrimRight(u, "/")+"/"+key)
		cmd.Stdin = tap
		if err := cmd.Start(); err != nil {
			t.Errorf("simulcast forwarder: %v", err)
			return func() {}
		}
		return func() {
			_ = tap.Close()
			_ = cmd.Process.Kill() // only the PID this test started
			_, _ = cmd.Process.Wait()
		}
	}
	t.Cleanup(fcf.StopForwards)

	// the processes
	fdPort := e2eFreePort(t)
	ports := map[string]int{"blue": e2eFreePort(t), "green": e2eFreePort(t), "amber": e2eFreePort(t)}
	cache := filepath.Join(t.TempDir(), "cache")
	noNode := filepath.Join(t.TempDir(), "no-node.json")
	fdBase := fmt.Sprintf("http://127.0.0.1:%d", fdPort)
	common := []string{"DATABASE_URL=" + dsn, "MAKTABA_NODE_CONFIG=" + noNode, "MAKTABA_FFMPEG_BIN=ffmpeg",
		"MAKTABA_FFPROBE_BIN=ffprobe", "MAKTABA_HEALTH_WARM=0s", "MAKTABA_JWKS_URL=", "MAKTABA_LOG_LEVEL=info",
		"MAKTABA_TV_HOST_ROLE=home", "MAKTABA_CLUSTER_ROLE=home", "MAKTABA_CACHE_ROOT=" + cache,
		"MAKTABA_PUBLIC_BASE_URL=" + fdBase}
	base := func(name string) string { return fmt.Sprintf("http://127.0.0.1:%d", ports[name]) }
	procs := map[string]*e2eProc{}
	runs := map[string]int{}
	startSlot := func(name, release string) {
		env := append(append([]string{}, common...), fmt.Sprintf("MAKTABA_HTTP_ADDR=127.0.0.1:%d", ports[name]),
			fmt.Sprintf("MAKTABA_ADMIN_ADDR=127.0.0.1:%d", e2eFreePort(t)))
		if release != "" {
			env = append(env, "MAKTABA_TV_RELEASE="+release)
		}
		runs[name]++
		procs[name] = startSlotProc(t, bin, fmt.Sprintf("slot-%s-%d", name, runs[name]), []string{"slot", "--slot", name}, env)
	}
	restart := func(name, release string) {
		procs[name].kill()
		startSlot(name, release)
	}
	startSlot("blue", "") // binds to the live release (tv_routing) at its start
	startSlot("green", "")
	startSlot("amber", r2) // the staged release
	fd := startSlotProc(t, bin, "frontdoor", []string{"serve"}, append(append([]string{}, common...),
		fmt.Sprintf("MAKTABA_HTTP_ADDR=127.0.0.1:%d", fdPort),
		fmt.Sprintf("MAKTABA_ADMIN_ADDR=127.0.0.1:%d", e2eFreePort(t)),
		fmt.Sprintf("MAKTABA_STREAMING_GRPC_ADDR=127.0.0.1:%d", e2eFreePort(t)),
		fmt.Sprintf("MAKTABA_TV_SLOTS=blue=127.0.0.1:%d,green=127.0.0.1:%d,amber=127.0.0.1:%d",
			ports["blue"], ports["green"], ports["amber"])))
	fdLog := func() []byte { b, _ := os.ReadFile(fd.log); return b }
	e2eWait(t, "the front door's push relay", 120*time.Second, func() bool {
		return bytes.Contains(fdLog(), []byte("tv_push_relay_ready"))
	})
	e2eWait(t, "blue live, green the standby", 60*time.Second, func() bool {
		b, okB := getSlotStatus(base("blue"))
		g, okG := getSlotStatus(base("green"))
		return okB && okG && b.Role == "live" && g.Role == "standby"
	})
	if st, _ := getSlotStatus(base("blue")); st.Rel != r1 {
		t.Fatalf("blue airs release %q, want the live release %s", st.Rel, r1)
	}

	// the signed internal call the change controller's staging (story 33.5) makes:
	// amber, running the staged release, takes the staging role
	var streamKey []byte
	e2eWait(t, "the cluster stream key", 30*time.Second, func() bool {
		return db.QueryRow(`SELECT stream_key FROM tv_cluster_secret WHERE id = 1`).Scan(&streamKey) == nil
	})
	internal := func(path string, body any) (int, string) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, fdBase+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Maktaba-Front", tvcluster.Sign(streamKey, http.MethodPost, path, time.Now().Add(time.Minute)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(out)
	}
	// story 33.5's staging start, as the change controller sends it: amber, up
	// on the staged release, takes the staging role and warms the watched channel
	prewarm := func(change string) {
		t.Helper()
		e2eWait(t, "amber up", 60*time.Second, func() bool {
			st, ok := getSlotStatus(base("amber"))
			return ok && st.Rel == r2
		})
		plan := map[string]any{"change_id": change, "release_id": r2, "version": "dev", "channels": []string{ch, ch2},
			"changed": []string{ch}, "probe": ch2, "probe_seconds": 5}
		if code, out := internal("/internal/tv/staging/start", plan); code != 200 {
			t.Fatalf("staging start: %d %s", code, out)
		}
		e2eWait(t, "amber warm on the channel", 90*time.Second, func() bool {
			resp, err := http.Get(base("amber") + "/stream/channel/" + ch + "/index.m3u8")
			if err != nil {
				return false
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return resp.StatusCode == 200 && strings.Count(string(body), "seg-") >= 3
		})
	}

	// viewers: HLS and TS on the watched channel, and the push
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e2eWait(t, "the channel's playlist", 90*time.Second, func() bool {
		resp, err := http.Get(fdBase + "/stream/channel/" + ch + "/index.m3u8")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == 200
	})
	hls := watchHLS(ctx, fdBase+"/stream/channel/"+ch+"/index.m3u8")
	ts := watchTS(ctx, fdBase+"/stream/channel/"+ch+"/live.ts")
	var pushID string
	if err := db.QueryRow(`INSERT INTO tv_remote_pushes (channel_id, state, ingest) VALUES ($1, 'live', 'rtmps') RETURNING id`, ch).Scan(&pushID); err != nil {
		t.Fatal(err)
	}
	mustExec(`INSERT INTO tv_push_secrets (push_id, url, key, expires_at) VALUES ($1, $2, $3, now() + interval '1 hour')`,
		pushID, ing.URL(), inKey)
	e2eWait(t, "5 s of pushed video at the ingest and the simulcast output", 90*time.Second, func() bool {
		_, v := publishTags(ing, inKey)
		_, o := publishTags(dst, simKey)
		return len(v) > 0 && v[len(v)-1].TS > 5000 && len(o) > 30
	})
	e2eWait(t, "the standby warm on the channel", 60*time.Second, func() bool {
		g, ok := getSlotStatus(base("green"))
		return ok && contains(g.Stations, ch)
	})
	t.Logf("set up in %v: blue live (release 1), green standby, amber up on release 2; HLS, TS and push running",
		time.Since(t0).Round(time.Second))

	// the change controller, as the API runs it; the pipeline's swap commit and
	// reverse commit are played by the queue (their SQL is the pipeline's, story
	// 33.2/33.9 tests cover it there)
	var qmu sync.Mutex
	var jobs []string
	commitSwap := func(change string) {
		var b time.Time
		if err := db.QueryRow(`SELECT end_at FROM channel_programs WHERE channel_id = $1 AND sched_gen = 0
			AND start_at <= now() AND end_at > now()`, ch).Scan(&b); err != nil {
			t.Errorf("block on air: %v", err)
			return
		}
		if time.Until(b) < 8*time.Second {
			b = b.Add(block) // the commit re-anchors when the boundary is too close for this test's viewers
		}
		tx, err := db.Begin()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = tx.Rollback() }()
		var op int64
		steps := []func() error{
			func() error {
				_, err := tx.Exec(`UPDATE tv_routing SET live_release_id = $1, live_gen = 1, previous_release_id = $2,
					previous_gen = 0, epoch = epoch + 1, switched_at = now(), previous_until = now() + interval '15 minutes'
					WHERE scope = 'cluster'`, r2, r1)
				return err
			},
			func() error {
				return tx.QueryRow(`INSERT INTO tv_swap_ops (change_id, kind, release_id, from_release_id, epoch, committed, created_by)
					SELECT $1, 'swap', $2, $3, epoch, true, 'job:tv_swap' FROM tv_routing WHERE scope = 'cluster' RETURNING id`,
					change, r2, r1).Scan(&op)
			},
			func() error {
				_, err := tx.Exec(`INSERT INTO tv_channel_switch (op_id, change_id, channel_id, switch_at) VALUES
					($1, $2, $3, $5), ($1, $2, $4, $5)`, op, change, ch, ch2, b)
				return err
			},
			func() error {
				_, err := tx.Exec(`UPDATE tv_changes SET swapped_at = now() WHERE id = $1`, change)
				return err
			},
		}
		for _, f := range steps {
			if err := f(); err != nil {
				t.Errorf("swap commit: %v", err)
				return
			}
		}
		if err := tx.Commit(); err != nil {
			t.Error(err)
		}
	}
	commitRollback := func(change string, payload map[string]any) {
		reason, _ := payload["reason"].(string)
		var op any
		if v, ok := payload["op_id"]; ok {
			op = v
		}
		for _, q := range []string{
			`UPDATE tv_routing SET live_release_id = previous_release_id, live_gen = previous_gen, previous_release_id = NULL,
				previous_gen = NULL, previous_until = NULL, epoch = epoch + 1, switched_at = now() WHERE scope = 'cluster'`,
		} {
			if _, err := db.Exec(q); err != nil {
				t.Errorf("reverse commit: %v", err)
			}
		}
		if op != nil {
			_, _ = db.Exec(`UPDATE tv_swap_ops SET committed = true WHERE id = $1`, op)
		}
		_, _ = db.Exec(`UPDATE tv_changes SET state = 'rolled_back', end_reason = $2, ended_at = now() WHERE id = $1`, change, reason)
	}
	ctl := tvchange.NewController(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctl.Interval = 500 * time.Millisecond
	ctl.Enqueue = func(_ context.Context, stage string, payload map[string]any, _ string) error {
		id, _ := payload["change_id"].(string)
		qmu.Lock()
		jobs = append(jobs, stage+":"+id)
		qmu.Unlock()
		switch stage {
		case tvchange.StageTVSwap:
			commitSwap(id)
		case tvchange.StageTVRollback:
			commitRollback(id, payload)
		}
		return nil
	}
	go ctl.Run(ctx)
	// a staged, verified change (ready); approve() is the admin's approval
	newChange := func() string {
		var id string
		if err := db.QueryRow(`INSERT INTO tv_changes (state, title, source, kinds, release_id, base_release_id, verify_report)
			VALUES ('ready', 'e2e', '{"kind": "manual"}', '["schedule"]', $1, $2, '{"warnings": 0}') RETURNING id`, r2, r1).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	approve := func(id string) {
		mustExec(`UPDATE tv_changes SET state = 'approved', approved_by = '["admin"]', approved_at = now() WHERE id = $1`, id)
	}
	changeState := func(id string) (state, reason string) {
		var r sql.NullString
		_ = db.QueryRow(`SELECT state, end_reason FROM tv_changes WHERE id = $1`, id).Scan(&state, &r)
		return state, r.String
	}
	// a phase's continuity: one discontinuity on the HLS side where the slot
	// changed, the TS connection still open and monotonic
	checkPhase := func(name string, fromSeg int, from, to string) (disc hlsEntry) {
		t.Helper()
		es := hls.entries()
		discs := 0
		var prev *hlsEntry
		if fromSeg > 0 && fromSeg <= len(es) {
			prev = &es[fromSeg-1]
		}
		for i := fromSeg; i < len(es); i++ {
			e := es[i]
			if i > 0 && e.seq != es[i-1].seq+1 {
				t.Errorf("%s: media sequence jumps %d → %d", name, es[i-1].seq, e.seq)
			}
			if e.disc && i >= fromSeg && i > 0 {
				discs++
				disc = e
				if prev != nil {
					gap := e.pdt.Sub(prev.pdt.Add(time.Duration(prev.dur * float64(time.Second))))
					if gap < -40*time.Millisecond || gap > 40*time.Millisecond {
						var around []string
						for j := i - 3; j <= i+2 && j < len(es); j++ {
							if j >= 0 {
								around = append(around, fmt.Sprintf("%d %s %s %.3f disc=%v", es[j].seq, es[j].uri,
									es[j].pdt.Format("15:04:05.000"), es[j].dur, es[j].disc))
							}
						}
						t.Errorf("%s: the first %s segment starts %v off the last %s one's end: %s", name, to, gap, from,
							strings.Join(around, " | "))
					}
					if !strings.HasPrefix(prev.uri, "s/"+from+"/") || !strings.HasPrefix(e.uri, "s/"+to+"/") {
						t.Errorf("%s: discontinuity between %s and %s (want %s → %s)", name, prev.uri, e.uri, from, to)
					}
				}
			}
			prev = &es[i]
		}
		if discs != 1 {
			t.Errorf("%s: %d discontinuities in the stitched playlist, want exactly one", name, discs)
		}
		select {
		case err := <-ts.done:
			t.Fatalf("%s: the TS viewer's connection ended: %v", name, err)
		default:
		}
		return disc
	}
	splices := func() []string {
		re := regexp.MustCompile(`tv: TS connection spliced[^\n]*`)
		return re.FindAllString(string(fdLog()), -1)
	}

	// ---- 1. the swap: the watched channel at its boundary ----------------------
	c1 := newChange()
	prewarm(c1)
	segs0 := len(hls.entries())
	frames0, _, _ := ts.stats(0)
	approve(c1)
	tSwap := time.Now()
	var plannedAt time.Time
	e2eWait(t, "the swap committed", 30*time.Second, func() bool {
		return db.QueryRow(`SELECT s.switch_at FROM tv_channel_switch s JOIN tv_swap_ops o ON o.id = s.op_id
			WHERE o.change_id = $1 AND s.channel_id = $2`, c1, ch).Scan(&plannedAt) == nil
	})
	e2eWait(t, "the change live", 90*time.Second, func() bool {
		s, _ := changeState(c1)
		return s == tvchange.StateLive
	})
	e2eWait(t, "HLS on amber", 30*time.Second, func() bool { return hls.lastSlot() == "amber" })
	time.Sleep(4 * time.Second)
	disc := checkPhase("swap", segs0, "blue", "amber")
	if d := disc.pdt.Sub(plannedAt); d > 0 || d < -2100*time.Millisecond {
		t.Errorf("swap: the HLS discontinuity is at %v, %v from the boundary %v (want the segment holding it)",
			disc.pdt.Format(time.RFC3339Nano), d, plannedAt.Format(time.RFC3339Nano))
	}
	var quietLate float64
	var started time.Time
	if err := db.QueryRow(`SELECT EXTRACT(EPOCH FROM s.switched_at - o.started_at), o.started_at FROM tv_channel_switch s
		JOIN tv_swap_ops o ON o.id = s.op_id WHERE o.change_id = $1 AND s.channel_id = $2 AND s.state = 'switched'`,
		c1, ch2).Scan(&quietLate, &started); err != nil || quietLate > 3 {
		t.Errorf("the unwatched channel switched %.1f s after the operation started (%v), want at once", quietLate, err)
	}
	if b, _ := getSlotStatus(base("blue")); b.Role != "previous" {
		t.Errorf("after the swap blue is %q, want previous (the warm rollback target)", b.Role)
	}
	if a, _ := getSlotStatus(base("amber")); a.Role != "live" {
		t.Errorf("after the swap amber is %q", a.Role)
	}
	sp := splices()
	frames1, back, maxStep := ts.stats(frames0)
	if len(sp) == 0 || !strings.Contains(sp[len(sp)-1], "mode=seamless") || back > 0 || maxStep > 2*3000 {
		t.Errorf("swap TS: splices %v; %d timestamps not increasing, largest step %.3f s", sp, back, float64(maxStep)/90000)
	}
	t.Logf("swap: the programme boundary %v after the approval, %s; HLS discontinuity at %s; TS %s; unwatched channel switched %.2f s after the start",
		plannedAt.Sub(tSwap).Round(time.Second), plannedAt.Format("15:04:05.000"), disc.pdt.Format("15:04:05.000"),
		lastField(sp), quietLate)

	// ---- 2. the warm rollback, at once ------------------------------------------
	segs1 := len(hls.entries())
	if pt, err := ctl.Point(ctx); err != nil || pt.Mode != "warm" || pt.Slot != "blue" {
		t.Fatalf("rollback point: %+v %v", pt, err)
	}
	admin := "00000000-0000-4000-8000-0000000000ad"
	tRB := time.Now()
	res, err := ctl.Rollback(ctx, &admin, "")
	if err != nil || res.Mode != "warm" || res.OpID == 0 {
		t.Fatalf("warm rollback: %+v %v", res, err)
	}
	e2eWait(t, "the change rolled back", 30*time.Second, func() bool {
		s, _ := changeState(c1)
		return s == tvchange.StateRolledBack
	})
	e2eWait(t, "HLS back on blue", 30*time.Second, func() bool { return hls.lastSlot() == "blue" })
	rbAfter := time.Since(tRB)
	e2eWait(t, "blue live again after the rollback", 30*time.Second, func() bool {
		b, ok := getSlotStatus(base("blue"))
		return ok && b.Role == "live"
	})
	time.Sleep(2 * time.Second)
	checkPhase("warm rollback", segs1, "amber", "blue")
	_, back, maxStep = ts.stats(frames1)
	frames2, _, _ := ts.stats(0)
	if back > 0 || maxStep > 2*90000 {
		t.Errorf("warm rollback TS: %d timestamps not increasing, largest step %.3f s", back, float64(maxStep)/90000)
	}
	t.Logf("warm rollback: blue on the air again, the HLS viewer on blue %v after the request; TS %s",
		rbAfter.Round(100*time.Millisecond), lastField(splices()))

	// ---- 3. a crash of the new live slot inside the grace: automatic rollback ---
	restart("green", "") // the supervisor's restarts: the standby, and amber on the staged release
	restart("amber", r2)
	c2 := newChange()
	prewarm(c2)
	segs2 := len(hls.entries())
	approve(c2)
	e2eWait(t, "the second swap live", 90*time.Second, func() bool {
		s, _ := changeState(c2)
		return s == tvchange.StateLive
	})
	e2eWait(t, "HLS on amber again", 30*time.Second, func() bool { return hls.lastSlot() == "amber" })
	e2eWait(t, "amber live, blue previous", 30*time.Second, func() bool {
		a, okA := getSlotStatus(base("amber"))
		b, okB := getSlotStatus(base("blue"))
		return okA && okB && a.Role == "live" && b.Role == "previous"
	})
	time.Sleep(2 * time.Second)
	checkPhase("second swap", segs2, "blue", "amber")
	segs3 := len(hls.entries())
	frames3, _, _ := ts.stats(0)
	killed := time.Now()
	procs["amber"].kill() // only the PID this test started
	e2eWait(t, "HLS back on blue after the crash", 30*time.Second, func() bool { return hls.lastSlot() == "blue" })
	autoAfter := time.Since(killed)
	e2eWait(t, "the change rolled back automatically", 30*time.Second, func() bool {
		s, r := changeState(c2)
		return s == tvchange.StateRolledBack && r == "auto:crash"
	})
	var routedBack time.Duration
	e2eWait(t, "blue live again (the operation settled)", 30*time.Second, func() bool {
		b, ok := getSlotStatus(base("blue"))
		if ok && b.Role == "live" {
			routedBack = time.Since(killed)
			return true
		}
		return false
	})
	time.Sleep(2 * time.Second)
	checkPhase("automatic rollback", segs3, "amber", "blue")
	_, back, maxStep = ts.stats(frames3)
	if back > 0 || maxStep > 2*90000 {
		t.Errorf("automatic rollback TS: %d timestamps not increasing, largest step %.3f s", back, float64(maxStep)/90000)
	}
	if !bytes.Contains(fdLog(), []byte("tv_rollback_auto")) {
		t.Error("the front door logged no automatic rollback")
	}
	detected := time.Duration(-1)
	if m := regexp.MustCompile(`ts=(\S+) [^\n]*event=tv_rollback_auto`).FindSubmatch(fdLog()); m != nil {
		if at, err := time.Parse(time.RFC3339Nano, string(m[1])); err == nil {
			detected = at.Sub(killed)
		}
	}
	if detected < 0 || detected > 10*time.Second {
		t.Errorf("automatic rollback: routed back %v after the kill (want within 10 s)", detected)
	}
	t.Logf("automatic rollback: the front door routed every channel back %v after amber was killed; HLS on blue %v after; "+
		"blue live (the operation settled) %v after; the change rolled_back (auto:crash)",
		detected.Round(10*time.Millisecond), autoAfter.Round(100*time.Millisecond), routedBack.Round(100*time.Millisecond))

	// ---- 4. a crash of the live slot with an unapproved change staged ----------
	restart("green", "")
	restart("amber", r2)
	c3 := newChange() // staged and verified, not approved
	prewarm(c3)
	e2eWait(t, "green the standby again, warm", 90*time.Second, func() bool {
		g, ok := getSlotStatus(base("green"))
		return ok && g.Role == "standby" && contains(g.Stations, ch) && g.Rel == r1
	})
	time.Sleep(6 * time.Second) // a few segments on the grid in the restarted standby
	segs4 := len(hls.entries())
	procs["blue"].kill()
	e2eWait(t, "green live", 30*time.Second, func() bool {
		g, ok := getSlotStatus(base("green"))
		return ok && g.Role == "live"
	})
	e2eWait(t, "HLS on green", 30*time.Second, func() bool { return hls.lastSlot() == "green" })
	time.Sleep(3 * time.Second)
	checkPhase("failover with a staged change", segs4, "blue", "green")
	if a, _ := getSlotStatus(base("amber")); a.Role != "staging" {
		t.Errorf("the staging slot became %q in the crash", a.Role)
	}
	if s, _ := changeState(c3); s != tvchange.StateReady {
		t.Errorf("the unapproved change is %s after the crash, want ready", s)
	}
	for _, e := range hls.entries()[segs4:] {
		if strings.HasPrefix(e.uri, "s/amber/") {
			t.Fatalf("a viewer was routed to the staging slot: %s", e.uri)
		}
	}

	// ---- across everything: one push session, one simulcast session -----------
	time.Sleep(2 * time.Second)
	pubs, v := publishTags(ing, inKey)
	if len(pubs) != 1 || !pubs[0].End.IsZero() || ing.Active(inKey) != 1 {
		t.Fatalf("ingest sessions: %d (want one, still connected)", len(pubs))
	}
	gap, back := maxGapMS(v)
	if back > 0 || gap > 2000 {
		t.Errorf("ingest video: %d timestamps going back, largest gap %d ms (want ≤ one keyframe interval, 2000)", back, gap)
	}
	opubs, ov := publishTags(dst, simKey)
	ogap, oback := maxGapMS(ov)
	fwdMu.Lock()
	nfwd := forwards
	fwdMu.Unlock()
	if len(opubs) != 1 || !opubs[0].End.IsZero() || dst.Active(simKey) != 1 || nfwd != 1 {
		t.Fatalf("simulcast output sessions: %d, retransmissions started %d (want one, never disconnected)", len(opubs), nfwd)
	}
	if oback > 0 || ogap > 2000 {
		t.Errorf("simulcast output video: %d timestamps going back, largest gap %d ms", oback, ogap)
	}
	frames, back, maxStep := ts.stats(frames2)
	if back > 0 {
		t.Errorf("TS: %d timestamps not increasing", back)
	}
	hlsBad := 0
	for _, e := range hls.entries() {
		if !e.ok {
			hlsBad++
		}
	}
	hls.mu.Lock()
	if hls.err != "" {
		t.Error(hls.err)
	}
	hls.mu.Unlock()
	switches := bytes.Count(fdLog(), []byte("tv_push_source_switch"))
	qmu.Lock()
	t.Logf("across two swaps, two rollbacks and a failover: ingest one session, %d video tags, largest gap %d ms; "+
		"simulcast output one session, %d video tags, largest gap %d ms; relay source switches %d; TS one connection, %d frames, largest step %.3f s; "+
		"HLS %d segments (%d not fetched); jobs %v; total %v",
		len(v), gap, len(ov), ogap, switches, frames, float64(maxStep)/90000, len(hls.entries()), hlsBad, jobs,
		time.Since(t0).Round(time.Second))
	qmu.Unlock()
}

// lastField is the last splice log line's mode and lateness.
func lastField(lines []string) string {
	if len(lines) == 0 {
		return "(no splice logged)"
	}
	l := lines[len(lines)-1]
	out := []string{}
	for _, k := range []string{"mode=", "late_ms="} {
		if i := strings.Index(l, k); i >= 0 {
			f := strings.Fields(l[i:])
			out = append(out, f[0])
		}
	}
	return strings.Join(out, " ")
}
