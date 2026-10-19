package tvremote_test

// Epic 33 stories 33.1 and 33.4 end to end on one machine, with real processes
// and real ffmpeg: the home's front door (maktaba-streaming serve with
// MAKTABA_TV_SLOTS) and two TV slots (maktaba-streaming slot --slot blue|green)
// against a scratch Postgres, an HLS viewer, a TS (HDHomeRun-style) viewer and
// a push to a fake RTMPS ingest (rtmptest) through the front door's relay. It
// shows:
//
//   - the front door routes the channel to the live slot (blue) and the hot
//     standby (green) warms the watched channel by itself;
//   - the two slots' output of the channel is splice-ready (33.4): the same
//     segment boundaries and program dates, the same first PTS, the same PIDs;
//   - killing the live slot mid-stream fails every viewer over to the standby:
//     the HLS viewer's playlist continues with one EXT-X-DISCONTINUITY, no
//     media-sequence reset and the next segment starting where the last one
//     ended; the TS viewer's connection stays open and its timestamps stay
//     monotonic; the push keeps its one ingest session with monotonic
//     timestamps;
//   - the killed instance, started again, comes back as the new standby.
//
// Runs only with MAKTABA_TEST_PG_URL and ffmpeg on PATH; never in -short.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/rtmptest"
)

func startSlotProc(t *testing.T, bin, name string, args, env []string) *e2eProc {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), name+".log")
	lf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = lf, lf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &e2eProc{cmd: cmd, log: logPath}
	t.Cleanup(func() {
		p.kill()
		_ = lf.Close()
		if t.Failed() {
			b, _ := os.ReadFile(logPath)
			lines := strings.Split(string(b), "\n")
			if len(lines) > 60 {
				lines = lines[len(lines)-60:]
			}
			t.Logf("--- %s log (tail) ---\n%s", name, strings.Join(lines, "\n"))
		}
	})
	return p
}

// slotStatus is what GET /internal/tv/slot/status answers (the fields used).
type slotStatus struct {
	Slot     string   `json:"slot"`
	Role     string   `json:"role"`
	Epoch    int64    `json:"epoch"`
	Stations []string `json:"stations"`
	Rel      string   `json:"release"`
	PMTPID   int      `json:"pmt_pid"`
	VideoPID int      `json:"video_pid"`
	AudioPID int      `json:"audio_pid"`
}

func getSlotStatus(base string) (slotStatus, bool) {
	var st slotStatus
	resp, err := http.Get(base + "/internal/tv/slot/status")
	if err != nil {
		return st, false
	}
	defer resp.Body.Close()
	return st, resp.StatusCode == 200 && json.NewDecoder(resp.Body).Decode(&st) == nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// hlsEntry is one segment the HLS viewer saw.
type hlsEntry struct {
	seq  int64
	uri  string
	pdt  time.Time
	dur  float64
	disc bool
	at   time.Time
	ok   bool
}

// tsPTS reads the video PES timestamps (PID 0x100) of a TS byte stream.
type tsPTS struct {
	mu      sync.Mutex
	buf     []byte
	pts     []int64 // unwrapped
	at      []time.Time
	bytes   int64
	lastRaw int64
	base    int64
	have    bool
}

func (p *tsPTS) feed(b []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bytes += int64(len(b))
	p.buf = append(p.buf, b...)
	for len(p.buf) >= 188 {
		if p.buf[0] != 0x47 {
			p.buf = p.buf[1:]
			continue
		}
		pk := p.buf[:188]
		p.buf = p.buf[188:]
		pid := uint16(pk[1]&0x1f)<<8 | uint16(pk[2])
		if pid != 0x100 || pk[1]&0x40 == 0 {
			continue
		}
		off := 4
		if pk[3]&0x20 != 0 {
			off += 1 + int(pk[4])
		}
		if off+14 > 188 || pk[off] != 0 || pk[off+1] != 0 || pk[off+2] != 1 || pk[off+7]&0x80 == 0 {
			continue
		}
		q := pk[off+9:]
		raw := int64(q[0]>>1&0x07)<<30 | int64(q[1])<<22 | int64(q[2]>>1)<<15 | int64(q[3])<<7 | int64(q[4]>>1)
		if p.have {
			d := raw - p.lastRaw
			if d < -(1 << 32) {
				p.base += 1 << 33
			} else if d > 1<<32 {
				p.base -= 1 << 33
			}
		}
		p.lastRaw, p.have = raw, true
		p.pts = append(p.pts, raw+p.base)
		p.at = append(p.at, time.Now())
	}
}

func TestSlotsFailoverEndToEnd(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_PG_URL") == "" || testing.Short() {
		t.Skip("MAKTABA_TEST_PG_URL unset (or -short): skipping the TV slots end-to-end test")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	db, dsn := clustertest.ScratchDB(t)

	_, file, _, _ := runtime.Caller(0)
	streamingDir := filepath.Join(filepath.Dir(file), "..", "..", "..", "streaming")
	bin := filepath.Join(t.TempDir(), "maktaba-streaming")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = streamingDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build streaming: %v\n%s", err, out)
	}

	ch := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, 101, 'Slots', 'slots', 'shuffle')`, ch); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO app_settings (key, value, updated_at) VALUES
		('rules.tv_output', '{"encoder":"software","resolution":"854x480","fps":30,"video_kbps":800,"audio_kbps":64,"segment_seconds":2,"warm_grace_seconds":120}', now())`); err != nil {
		t.Fatal(err)
	}
	ing, err := rtmptest.Listen(true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ing.Close)

	fdPort, bluePort, greenPort := e2eFreePort(t), e2eFreePort(t), e2eFreePort(t)
	cache := filepath.Join(t.TempDir(), "cache")
	noNode := filepath.Join(t.TempDir(), "no-node.json")
	fdBase := fmt.Sprintf("http://127.0.0.1:%d", fdPort)
	common := []string{"DATABASE_URL=" + dsn, "MAKTABA_NODE_CONFIG=" + noNode, "MAKTABA_FFMPEG_BIN=ffmpeg",
		"MAKTABA_FFPROBE_BIN=ffprobe", "MAKTABA_HEALTH_WARM=0s", "MAKTABA_JWKS_URL=", "MAKTABA_LOG_LEVEL=info",
		"MAKTABA_TV_HOST_ROLE=home", "MAKTABA_CLUSTER_ROLE=home", "MAKTABA_CACHE_ROOT=" + cache,
		"MAKTABA_PUBLIC_BASE_URL=" + fdBase}
	blueBase := fmt.Sprintf("http://127.0.0.1:%d", bluePort)
	greenBase := fmt.Sprintf("http://127.0.0.1:%d", greenPort)
	slotEnv := func(port int) []string {
		return append(append([]string{}, common...), fmt.Sprintf("MAKTABA_HTTP_ADDR=127.0.0.1:%d", port),
			fmt.Sprintf("MAKTABA_ADMIN_ADDR=127.0.0.1:%d", e2eFreePort(t)))
	}
	blue := startSlotProc(t, bin, "slot-blue", []string{"slot", "--slot", "blue"}, slotEnv(bluePort))
	startSlotProc(t, bin, "slot-green", []string{"slot", "--slot", "green"}, slotEnv(greenPort))
	fd := startSlotProc(t, bin, "frontdoor", []string{"serve"}, append(append([]string{}, common...),
		fmt.Sprintf("MAKTABA_HTTP_ADDR=127.0.0.1:%d", fdPort),
		fmt.Sprintf("MAKTABA_ADMIN_ADDR=127.0.0.1:%d", e2eFreePort(t)),
		fmt.Sprintf("MAKTABA_STREAMING_GRPC_ADDR=127.0.0.1:%d", e2eFreePort(t)),
		fmt.Sprintf("MAKTABA_TV_SLOTS=blue=127.0.0.1:%d,green=127.0.0.1:%d", bluePort, greenPort)))

	e2eWait(t, "the front door's push relay", 120*time.Second, func() bool {
		b, _ := os.ReadFile(fd.log)
		return bytes.Contains(b, []byte("tv_push_relay_ready"))
	})
	e2eWait(t, "both slots in their roles", 60*time.Second, func() bool {
		b, okB := getSlotStatus(blueBase)
		g, okG := getSlotStatus(greenBase)
		return okB && okG && b.Role == "live" && g.Role == "standby"
	})
	t.Log("front door up; blue is live, green the hot standby (epoch 1)")

	// the HLS viewer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var hmu sync.Mutex
	seen := map[int64]*hlsEntry{}
	var order []int64
	hlsErr := ""
	t0 := time.Now()
	e2eWait(t, "the channel's playlist", 90*time.Second, func() bool {
		resp, err := http.Get(fdBase + "/stream/channel/" + ch + "/index.m3u8")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == 200
	})
	t.Logf("first playlist after %v", time.Since(t0).Round(100*time.Millisecond))
	go func() {
		for ctx.Err() == nil {
			resp, err := http.Get(fdBase + "/stream/channel/" + ch + "/index.m3u8")
			if err == nil {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode == 200 {
					var seq int64
					var cur hlsEntry
					idx := int64(0)
					for _, l := range strings.Split(string(body), "\n") {
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
							hmu.Lock()
							if e, ok := seen[n]; ok {
								if e.uri != l {
									hlsErr = fmt.Sprintf("media sequence %d was %s, now %s", n, e.uri, l)
								}
							} else {
								e := cur
								e.seq, e.uri, e.at = n, l, time.Now()
								seen[n] = &e
								order = append(order, n)
								go func(e *hlsEntry) {
									r, err := http.Get(fdBase + "/stream/channel/" + ch + "/" + e.uri)
									if err == nil {
										_, _ = io.Copy(io.Discard, r.Body)
										r.Body.Close()
										hmu.Lock()
										e.ok = r.StatusCode == 200
										hmu.Unlock()
									}
								}(&e)
							}
							hmu.Unlock()
							cur = hlsEntry{}
						}
					}
				}
			}
			time.Sleep(time.Second)
		}
	}()

	// the TS viewer (HDHomeRun-style)
	ts := &tsPTS{}
	tsDone := make(chan error, 1)
	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fdBase+"/stream/channel/"+ch+"/live.ts", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			tsDone <- err
			return
		}
		defer resp.Body.Close()
		r := bufio.NewReaderSize(resp.Body, 64<<10)
		buf := make([]byte, 32<<10)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				ts.feed(buf[:n])
			}
			if err != nil {
				tsDone <- err
				return
			}
		}
	}()

	// the push: what the API's controller writes for a live push
	var pushID string
	if err := db.QueryRow(`INSERT INTO tv_remote_pushes (channel_id, state, ingest) VALUES ($1, 'live', 'rtmps') RETURNING id`, ch).Scan(&pushID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tv_push_secrets (push_id, url, key, expires_at) VALUES ($1, $2, 'slots-key', now() + interval '1 hour')`,
		pushID, ing.URL()); err != nil {
		t.Fatal(err)
	}
	videoTags := func() []rtmptest.Tag {
		var out []rtmptest.Tag
		for _, p := range ing.Publishes() {
			for _, tg := range p.Tags {
				if tg.Type == 9 && !tg.SeqHeader {
					out = append(out, tg)
				}
			}
		}
		return out
	}
	e2eWait(t, "5 s of pushed video at the ingest", 60*time.Second, func() bool {
		v := videoTags()
		return len(v) > 0 && v[len(v)-1].TS > 5000
	})

	// the standby warms the watched channel by itself
	t1 := time.Now()
	e2eWait(t, "the standby to warm the channel", 60*time.Second, func() bool {
		g, ok := getSlotStatus(greenBase)
		return ok && contains(g.Stations, ch)
	})
	t.Logf("standby warm on the channel %v after it was on air", time.Since(t1).Round(100*time.Millisecond))
	time.Sleep(10 * time.Second) // a few segments in both slots

	// 33.4 across the two slots: the same segment boundaries, dates, PTS and PIDs
	type upSeg struct {
		name string
		pdt  string
	}
	playlist := func(base string) []upSeg {
		resp, err := http.Get(base + "/stream/channel/" + ch + "/index.m3u8")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var out []upSeg
		pdt := ""
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.TrimSpace(l)
			if v, ok := strings.CutPrefix(l, "#EXT-X-PROGRAM-DATE-TIME:"); ok {
				pdt = v
			} else if strings.HasPrefix(l, "seg-") {
				out = append(out, upSeg{l, pdt})
			}
		}
		return out
	}
	bl, gr := playlist(blueBase), playlist(greenBase)
	bluePDT := map[string]string{}
	for _, s := range bl {
		bluePDT[s.pdt] = s.name
	}
	common2 := 0
	for _, s := range gr {
		if s.pdt <= bl[len(bl)-1].pdt && s.pdt >= bl[0].pdt {
			if _, ok := bluePDT[s.pdt]; !ok {
				t.Errorf("green's segment %s (%s) starts where blue has no segment boundary", s.name, s.pdt)
			}
			common2++
		}
	}
	if common2 < 3 {
		t.Fatalf("only %d segments in common between blue %v and green %v", common2, bl, gr)
	}
	probe := func(path string) (string, string) {
		out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v", "-show_entries", "packet=pts",
			"-read_intervals", "%+#1", "-of", "csv=p=0", path).Output()
		if err != nil {
			t.Fatalf("ffprobe %s: %v", path, err)
		}
		ids, _ := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=id:program=pmt_pid", "-of", "compact", path).Output()
		return strings.TrimSpace(string(out)), string(ids)
	}
	last := gr[len(gr)-1]
	if bn, ok := bluePDT[last.pdt]; ok {
		pb, idb := probe(filepath.Join(cache, "tv", "blue", "channel-"+ch, bn))
		pg, idg := probe(filepath.Join(cache, "tv", "green", "channel-"+ch, last.name))
		if pb != pg || idb != idg {
			t.Fatalf("aligned segments differ: blue %s pts %s %s / green %s pts %s %s", bn, pb, idb, last.name, pg, idg)
		}
		t.Logf("33.4 across slots: %d segment boundaries in common, the same dates; segment at %s: first PTS %s in both, PIDs %s",
			common2, last.pdt, pb, strings.ReplaceAll(strings.TrimSpace(idb), "\n", " "))
	}
	if st, _ := getSlotStatus(blueBase); st.PMTPID != 0x1000 || st.VideoPID != 0x100 || st.AudioPID != 0x101 {
		t.Errorf("slot status PIDs: %+v", st)
	}

	// kill the live slot mid-stream
	hmu.Lock()
	beforeKill := len(order)
	hmu.Unlock()
	ts.mu.Lock()
	tsBefore, pesBefore := ts.bytes, len(ts.pts)
	ts.mu.Unlock()
	tagsBefore := len(videoTags())
	killedAt := time.Now()
	blue.kill() // only the PID this test started (its ffmpegs die with it: job object / pdeathsig)
	var failoverAfter time.Duration
	e2eWait(t, "green to be live", 30*time.Second, func() bool {
		g, ok := getSlotStatus(greenBase)
		if ok && g.Role == "live" {
			failoverAfter = time.Since(killedAt)
			return true
		}
		return false
	})
	var firstGreen *hlsEntry
	e2eWait(t, "HLS segments from green", 60*time.Second, func() bool {
		hmu.Lock()
		defer hmu.Unlock()
		n := 0
		for _, s := range order {
			if e := seen[s]; strings.HasPrefix(e.uri, "s/green/") {
				if firstGreen == nil {
					firstGreen = e
				}
				n++
			}
		}
		return n >= 4
	})
	t.Logf("failover: green live %v after the kill; first green segment in the playlist %v after",
		failoverAfter.Round(10*time.Millisecond), firstGreen.at.Sub(killedAt).Round(100*time.Millisecond))
	time.Sleep(6 * time.Second)

	// HLS: monotonic media sequence, one discontinuity at the switch, the first
	// green segment starts where the last blue one ended
	hmu.Lock()
	if hlsErr != "" {
		t.Error(hlsErr)
	}
	discs := 0
	var lastBlue *hlsEntry
	for i, s := range order {
		e := seen[s]
		if i > 0 && s != order[i-1]+1 {
			t.Errorf("media sequence jumps from %d to %d", order[i-1], s)
		}
		if e.disc && i > 0 {
			discs++
		}
		if strings.HasPrefix(e.uri, "s/blue/") {
			lastBlue = e
		}
	}
	if discs != 1 {
		t.Errorf("%d discontinuities in the stitched playlist, want exactly one", discs)
	}
	if !firstGreen.disc {
		t.Error("the first green segment carries no EXT-X-DISCONTINUITY")
	}
	gap := firstGreen.pdt.Sub(lastBlue.pdt.Add(time.Duration(lastBlue.dur * float64(time.Second))))
	if gap < -40*time.Millisecond || gap > 40*time.Millisecond {
		t.Errorf("the first green segment starts %v off the last blue one's end (%s + %.3f → %s)", gap,
			lastBlue.pdt.Format(time.RFC3339Nano), lastBlue.dur, firstGreen.pdt.Format(time.RFC3339Nano))
	}
	notOK := 0
	for _, s := range order[beforeKill:] {
		if !seen[s].ok {
			notOK++
		}
	}
	hmu.Unlock()
	if notOK > 1 {
		t.Errorf("%d segments after the kill did not load", notOK)
	}
	t.Logf("HLS: %d segments, one discontinuity, the green segment starts %v after the last blue one ends; %d segment fetches failed",
		len(order), gap, notOK)

	// TS: the connection stayed, data continues, timestamps monotonic
	select {
	case err := <-tsDone:
		t.Fatalf("the TS viewer's connection ended: %v", err)
	default:
	}
	ts.mu.Lock()
	grown := ts.bytes - tsBefore
	var back, maxStep int64
	for i := 1; i < len(ts.pts); i++ {
		d := ts.pts[i] - ts.pts[i-1]
		if d <= 0 {
			back++
		}
		if d > maxStep {
			maxStep = d
		}
	}
	tsFrames := len(ts.pts)
	ts.mu.Unlock()
	// 30 fps: after the kill (≥ 12 s waited above) at least 8 s of frames
	if tsFrames-pesBefore < 240 || back > 0 || maxStep > 90000 {
		t.Errorf("TS after the kill: %d frames, %d timestamps not increasing, largest step %.3f s",
			tsFrames-pesBefore, back, float64(maxStep)/90000)
	}
	t.Logf("TS: one connection, %d video PES (%d after the kill, %d bytes), monotonic, largest step %.3f s",
		tsFrames, tsFrames-pesBefore, grown, float64(maxStep)/90000)

	// push: one ingest session, monotonic, no big gap
	pubs := ing.Publishes()
	v := videoTags()
	if len(pubs) != 1 || !pubs[0].End.IsZero() || ing.Active(pubs[0].Key) != 1 {
		t.Fatalf("ingest sessions: %d (want one, still connected)", len(pubs))
	}
	var maxGap uint32
	for i := 1; i < len(v); i++ {
		if v[i].TS < v[i-1].TS {
			t.Fatalf("pushed video timestamp went back: %d after %d", v[i].TS, v[i-1].TS)
		}
		if g := v[i].TS - v[i-1].TS; g > maxGap {
			maxGap = g
		}
	}
	if len(v) < tagsBefore+60 || maxGap > 1000 {
		t.Errorf("push after the kill: %d new video tags, max gap %d ms", len(v)-tagsBefore, maxGap)
	}
	fb, _ := os.ReadFile(fd.log)
	if !bytes.Contains(fb, []byte("tv_slot_failover")) || !bytes.Contains(fb, []byte("tv_push_source_switch")) {
		t.Error("the front door logged no failover or relay source switch")
	}
	t.Logf("push: one ingest session, %d video tags, monotonic, max gap %d ms", len(v), maxGap)

	// the killed instance comes back as the new standby and warms the channel
	startSlotProc(t, bin, "slot-blue-2", []string{"slot", "--slot", "blue"}, slotEnv(bluePort))
	t2 := time.Now()
	e2eWait(t, "blue back as the standby, warm", 90*time.Second, func() bool {
		b, ok := getSlotStatus(blueBase)
		return ok && b.Role == "standby" && contains(b.Stations, ch)
	})
	var roles []string
	rows, err := db.Query(`SELECT slot || '=' || role || '@' || role_epoch FROM tv_slots ORDER BY slot`)
	if err == nil {
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			roles = append(roles, s)
		}
		rows.Close()
	}
	t.Logf("blue back as the hot standby, warm, %v after its restart; tv_slots %v", time.Since(t2).Round(100*time.Millisecond), roles)

	// AC7: the front door restarts; the slots keep their stations, and the first
	// playlist after its restart needs no new encode warm-up
	cancel() // the viewers end with the front door
	fd.kill()
	time.Sleep(3 * time.Second)
	g, _ := getSlotStatus(greenBase)
	if !contains(g.Stations, ch) {
		t.Fatalf("the live slot lost the channel when the front door went away: %+v", g)
	}
	startSlotProc(t, bin, "frontdoor-2", []string{"serve"}, append(append([]string{}, common...),
		fmt.Sprintf("MAKTABA_HTTP_ADDR=127.0.0.1:%d", fdPort),
		fmt.Sprintf("MAKTABA_ADMIN_ADDR=127.0.0.1:%d", e2eFreePort(t)),
		fmt.Sprintf("MAKTABA_STREAMING_GRPC_ADDR=127.0.0.1:%d", e2eFreePort(t)),
		fmt.Sprintf("MAKTABA_TV_SLOTS=blue=127.0.0.1:%d,green=127.0.0.1:%d", bluePort, greenPort)))
	e2eWait(t, "the restarted front door to listen", 60*time.Second, func() bool {
		resp, err := http.Get(fdBase + "/stream/channel/" + uuid.NewString() + "/index.m3u8")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return true
	})
	t3 := time.Now()
	resp, err := http.Get(fdBase + "/stream/channel/" + ch + "/index.m3u8")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("playlist after the front door's restart: %v %v", resp, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	first := time.Since(t3)
	if first > time.Second || !strings.Contains(string(body), "s/green/") {
		t.Errorf("first playlist %v after the front door's restart (want < 1 s, from green): %s", first, body)
	}
	t.Logf("front door restart: the first playlist in %v, from the live slot (green), no new encode", first.Round(time.Millisecond))
}
