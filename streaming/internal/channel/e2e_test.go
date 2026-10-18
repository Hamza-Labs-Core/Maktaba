package channel

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
)

// syncRepo is a goroutine-safe fake Repo for the real-ffmpeg test.
type syncRepo struct {
	mu     sync.Mutex
	blocks []ProgramBlock
	reads  int
}

func (r *syncRepo) ProgramsFrom(_ context.Context, _ uuid.UUID, from time.Time, limit int) ([]ProgramBlock, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	var out []ProgramBlock
	for _, b := range r.blocks {
		if b.EndAt.After(from) {
			out = append(out, b)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
func (r *syncRepo) Look(context.Context, uuid.UUID) (Look, error) { return DefaultLook(), nil }
func (r *syncRepo) SetRuntime(context.Context, Runtime) error     { return nil }
func (r *syncRepo) ClearRuntime(context.Context, uuid.UUID) error { return nil }

func needFFmpeg(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("real-ffmpeg test skipped in -short mode")
	}
	for _, b := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not on PATH", b)
		}
	}
}

func makeClip(t *testing.T, path string, withAudio bool, secs int) {
	t.Helper()
	d := strconv.Itoa(secs)
	args := []string{"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=s=640x360:r=25:d=" + d}
	if withAudio {
		args = append(args, "-f", "lavfi", "-i", "sine=f=440:sample_rate=44100:d="+d)
	}
	args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p")
	if withAudio {
		args = append(args, "-c:a", "aac")
	}
	args = append(args, path)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("make clip: %v\n%s", err, out)
	}
}

func probeStartTime(t *testing.T, path string) (float64, int) {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=start_time,nb_streams",
		"-of", "default=nw=1", path).Output()
	if err != nil {
		return -1, 0
	}
	var st float64 = -1
	var ns int
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if v, ok := strings.CutPrefix(l, "start_time="); ok {
			st, _ = strconv.ParseFloat(v, 64)
		}
		if v, ok := strings.CutPrefix(l, "nb_streams="); ok {
			ns, _ = strconv.Atoi(v)
		}
	}
	return st, ns
}

var segRe = regexp.MustCompile(`^seg-(\d+)\.ts$`) // completed segments only (temp_file)

// TestStation_RealFFmpeg_ContinuousWallClock runs the whole pipeline with
// real ffmpeg over program (with audio) → filler (silent file) → slate →
// program and checks the output never stops between blocks and keeps
// wall-clock time.
func TestStation_RealFFmpeg_ContinuousWallClock(t *testing.T) {
	needFFmpeg(t)
	tmp := t.TempDir()
	clipA := filepath.Join(tmp, "a with audio.mp4")
	clipB := filepath.Join(tmp, "b silent.mp4")
	makeClip(t, clipA, true, 12)
	makeClip(t, clipB, false, 12)

	now := time.Now()
	b := func(seq int64, kind, path string, from, to time.Duration) ProgramBlock {
		return ProgramBlock{Seq: seq, Kind: kind, VideoID: uuid.New(), Path: path,
			StartAt: now.Add(from), EndAt: now.Add(to), SourceDurationMS: int((to - from) / time.Millisecond)}
	}
	repo := &syncRepo{blocks: []ProgramBlock{
		b(1, KindProgram, clipA, -2*time.Second, 6*time.Second), // joined 2 s in
		b(2, KindFiller, clipB, 6*time.Second, 12*time.Second),  // no audio → anullsrc
		{Seq: 3, Kind: KindSlate, StartAt: now.Add(12 * time.Second), EndAt: now.Add(17 * time.Second)},
		b(4, KindProgram, clipA, 17*time.Second, 40*time.Second),
	}}

	prof := OutputProfile{Width: 640, Height: 360, FPS: 30, VideoKbps: 1200, AudioCodec: "aac",
		AudioKbps: 128, Encoder: EncoderSoftware, SegmentSeconds: 2, HLSListSize: 60}
	dir := filepath.Join(tmp, "hls")
	st, err := StartStation(StationConfig{
		FFmpeg: "ffmpeg", Profile: prof, Repo: repo, Prober: NewFFprobe("ffprobe"),
		Resolver: NewYtDlp(""), PathMap: pathmap.Parse(""), FontFile: FindFont(),
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}, uuid.New(), dir)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		st.Stop()
		<-st.Done()
	}()

	// A TS fan-out client.
	var tsBytes bytes.Buffer
	var tsMu sync.Mutex
	sub, unsub := st.Subscribe()
	defer unsub()
	go func() {
		for chunk := range sub {
			tsMu.Lock()
			tsBytes.Write(chunk)
			tsMu.Unlock()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	if err := st.WaitReady(ctx); err != nil {
		cancel()
		logs, _ := os.ReadFile(filepath.Join(dir, "mux.log"))
		blogs, _ := os.ReadFile(filepath.Join(dir, "blocks.log"))
		t.Fatalf("first segment: %v\nmux.log:\n%s\nblocks.log:\n%s", err, logs, blogs)
	}
	cancel()

	firstSeen := map[int]time.Time{}
	deadline := time.Now().Add(22 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if m := segRe.FindStringSubmatch(e.Name()); m != nil {
				n, _ := strconv.Atoi(m[1])
				if _, ok := firstSeen[n]; !ok {
					firstSeen[n] = time.Now()
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	blogs, _ := os.ReadFile(filepath.Join(dir, "blocks.log"))
	mlogs, _ := os.ReadFile(filepath.Join(dir, "mux.log"))
	t.Logf("blocks.log:\n%s\nmux.log:\n%s", blogs, mlogs)

	var nums []int
	for n := range firstSeen {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	if len(nums) < 8 {
		t.Fatalf("only %d segments in 22 s; output stalled", len(nums))
	}
	// Never stops between blocks: new segments keep appearing.
	for i := 1; i < len(nums); i++ {
		if gap := firstSeen[nums[i]].Sub(firstSeen[nums[i-1]]); gap > 6*time.Second {
			t.Errorf("segment %d appeared %v after %d — output stalled", nums[i], gap, nums[i-1])
		}
	}
	// Wall-clock: segment timestamps advance like the clock.
	first, last := nums[1], nums[len(nums)-2] // skip the possibly-partial edges
	ptsFirst, _ := probeStartTime(t, filepath.Join(dir, "seg-"+strconv.Itoa(first)+".ts"))
	ptsLast, streams := probeStartTime(t, filepath.Join(dir, "seg-"+strconv.Itoa(last)+".ts"))
	if ptsFirst < 0 || ptsLast < 0 {
		t.Fatalf("could not probe segments %d/%d", first, last)
	}
	dPTS := ptsLast - ptsFirst
	dWall := firstSeen[last].Sub(firstSeen[first]).Seconds()
	t.Logf("segments %d..%d: Δpts=%.2fs Δwall=%.2fs", first, last, dPTS, dWall)
	// Wall clock: each segment's lag (when it appeared vs its timestamp)
	// must be constant once the channel is up — blocks are queued ahead
	// of their boundary, but forwarding is paced to the clock, so the
	// output neither drifts nor jumps at block boundaries. (The first
	// segments carry the start-up latency and are skipped.)
	// A segment file completes when the muxer receives the keyframe that
	// ends it, so lag = completion time − segment end (timeline).
	minLag, maxLag := 1e9, -1e9
	prevLag, maxStep := 0.0, 0.0
	for i, n := range nums[3 : len(nums)-1] {
		end, ok := probeVideoEnd(filepath.Join(dir, "seg-"+strconv.Itoa(n)+".ts"))
		if !ok {
			continue
		}
		lag := firstSeen[n].Sub(st.Epoch()).Seconds() - end
		t.Logf("seg %d end_pts=%.3f completed=%.3f lag=%.3f", n, end, firstSeen[n].Sub(st.Epoch()).Seconds(), lag)
		minLag, maxLag = min(minLag, lag), max(maxLag, lag)
		if i > 0 {
			maxStep = max(maxStep, abs(lag-prevLag))
		}
		prevLag = lag
	}
	t.Logf("max lag change between consecutive segments: %.3fs", maxStep)
	if maxStep > 0.2 {
		t.Errorf("segment timing jumps by %.3fs between consecutive segments", maxStep)
	}
	t.Logf("steady-state segment lag spread: %.3fs (%.3f..%.3f)", maxLag-minLag, minLag, maxLag)
	if maxLag-minLag > 0.3 {
		t.Errorf("segment timing drifts vs the wall clock by %.3fs", maxLag-minLag)
	}

	// Gapless: every segment starts exactly where the previous one ended
	// (pts continuity across all block boundaries, within 100 ms).
	pl, _ := readShared(filepath.Join(dir, "index.m3u8"))
	names, durs := parsePlaylist(string(pl))
	if len(names) < 8 {
		t.Fatalf("playlist has %d segments", len(names))
	}
	prevStart, prevDur := -1.0, 0.0
	maxJump := 0.0
	for i, name := range names {
		st, _ := probeStartTime(t, filepath.Join(dir, name))
		if st < 0 {
			continue
		}
		if prevStart >= 0 {
			jump := st - (prevStart + prevDur)
			if jump < 0 {
				jump = -jump
			}
			if jump > maxJump {
				maxJump = jump
			}
			if jump > 0.1 {
				t.Errorf("segment %s starts %.3fs off the previous segment's end", name, st-(prevStart+prevDur))
			}
		}
		prevStart, prevDur = st, durs[i]
	}
	t.Logf("max pts discontinuity between consecutive segments: %.3fs over %d segments", maxJump, len(names))

	// Wall clock: every block after the join starts exactly at its
	// scheduled position on the channel timeline.
	offRe := regexp.MustCompile(`off=([0-9.]+)`)
	var offs []float64
	for _, m := range offRe.FindAllStringSubmatch(string(blogs), -1) {
		v, _ := strconv.ParseFloat(m[1], 64)
		offs = append(offs, v)
	}
	ep := st.Epoch()
	for i, b := range repo.blocks[1:] {
		want := b.StartAt.Sub(ep).Seconds()
		if i+1 >= len(offs) {
			break
		}
		if d := offs[i+1] - want; d > 0.002 || d < -0.002 {
			t.Errorf("block %d timeline offset %.3f, scheduled %.3f", b.Seq, offs[i+1], want)
		}
	}
	t.Logf("non-monotonic DTS warnings in mux.log: %d", strings.Count(string(mlogs), "Non-monotonic"))
	// Timeline position matches the schedule: pts since epoch ≈ wall since epoch.
	if streams != 2 {
		t.Errorf("segment %d has %d streams, want video+audio", last, streams)
	}
	// Every block kind aired.
	for _, want := range []string{"file", "slate"} {
		if !strings.Contains(string(blogs), " "+want+" ") {
			t.Errorf("blocks.log has no %s block", want)
		}
	}
	if strings.Count(string(blogs), "b silent.mp4") == 0 {
		t.Error("silent filler never aired")
	}
	tsMu.Lock()
	n := tsBytes.Len()
	head := tsBytes.Bytes()
	tsMu.Unlock()
	if n < 500_000 {
		t.Errorf("TS fan-out client got only %d bytes", n)
	}
	if n > 0 && head[0] != 0x47 {
		t.Errorf("TS fan-out not packet aligned (first byte %#x)", head[0])
	}
	if n%tsPacket != 0 {
		t.Errorf("TS fan-out delivered a partial packet (%d bytes)", n)
	}
}

// TestEngine_RealFFmpeg_LazyHLSAndSharedTS drives the HTTP surface: the
// first index.m3u8 request tunes the channel and waits for a segment, a
// live.ts client shares the same encode.
func TestEngine_RealFFmpeg_LazyHLSAndSharedTS(t *testing.T) {
	needFFmpeg(t)
	tmp := t.TempDir()
	clip := filepath.Join(tmp, "clip.mp4")
	makeClip(t, clip, true, 30)
	now := time.Now()
	repo := &syncRepo{blocks: []ProgramBlock{{Seq: 1, Kind: KindProgram, VideoID: uuid.New(), Path: clip,
		StartAt: now.Add(-5 * time.Second), EndAt: now.Add(60 * time.Second), SourceDurationMS: 65_000}}}
	prof := OutputProfile{Width: 640, Height: 360, FPS: 30, VideoKbps: 1000, Encoder: EncoderSoftware, SegmentSeconds: 2}
	starts := 0
	base := StationStarter(StationConfig{FFmpeg: "ffmpeg", Profile: prof, Repo: repo, Prober: NewFFprobe("ffprobe"),
		Resolver: NewYtDlp(""), Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))})
	e := NewEngine(repo, fakeLayout{root: tmp}, func(ctx context.Context, id uuid.UUID, dir string) (Pipeline, error) {
		starts++
		return base(ctx, id, dir)
	}, 2, nil)
	defer e.StopAll()

	r := chi.NewRouter()
	(&HTTPHandler{Engine: e}).Mount(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	id := uuid.New()
	u := srv.URL + "/stream/channel/" + id.String()

	t0 := time.Now()
	resp, err := http.Get(u + "/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, "#EXTINF") {
		t.Fatalf("index.m3u8: %d %s", resp.StatusCode, body)
	}
	t.Logf("first playlist after %v", time.Since(t0))
	seg := regexp.MustCompile(`seg-\d+\.ts`).FindString(body)
	resp, err = http.Get(u + "/" + seg)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("segment %s: %v", seg, err)
	}
	if b := readAll(t, resp); len(b) < tsPacket || b[0] != 0x47 {
		t.Errorf("segment is not MPEG-TS (%d bytes)", len(b))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", u+"/live.ts", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	_, _ = got.ReadFrom(resp.Body)
	resp.Body.Close()
	if got.Len() < 100_000 || got.Bytes()[0] != 0x47 {
		t.Errorf("live.ts delivered %d bytes", got.Len())
	}
	if starts != 1 {
		t.Errorf("HLS + TS viewers started %d encodes, want 1", starts)
	}
}

// parsePlaylist returns segment names and EXTINF durations.
func parsePlaylist(pl string) ([]string, []float64) {
	var names []string
	var durs []float64
	var d float64
	for _, l := range strings.Split(pl, "\n") {
		l = strings.TrimSpace(l)
		if v, ok := strings.CutPrefix(l, "#EXTINF:"); ok {
			d, _ = strconv.ParseFloat(strings.TrimSuffix(v, ","), 64)
			continue
		}
		if strings.HasSuffix(l, ".ts") {
			names = append(names, l)
			durs = append(durs, d)
		}
	}
	return names, durs
}

// TestStation_RealFFmpeg_MuxerRestart kills the muxer mid-stream: the
// supervisor must restart it on its own, the HLS window keeps growing on
// the same timeline and TS clients stay connected.
func TestStation_RealFFmpeg_MuxerRestart(t *testing.T) {
	needFFmpeg(t)
	tmp := t.TempDir()
	clip := filepath.Join(tmp, "clip.mp4")
	makeClip(t, clip, true, 40)
	now := time.Now()
	repo := &syncRepo{blocks: []ProgramBlock{{Seq: 1, Kind: KindProgram, VideoID: uuid.New(), Path: clip,
		StartAt: now, EndAt: now.Add(40 * time.Second), SourceDurationMS: 40_000}}}
	prof := OutputProfile{Width: 640, Height: 360, FPS: 30, VideoKbps: 1000, Encoder: EncoderSoftware,
		SegmentSeconds: 2, HLSListSize: 60}
	dir := filepath.Join(tmp, "hls")
	st, err := StartStation(StationConfig{FFmpeg: "ffmpeg", Profile: prof, Repo: repo, Prober: NewFFprobe("ffprobe"),
		Resolver: NewYtDlp(""), Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))},
		uuid.New(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { st.Stop(); <-st.Done() }()

	var tsMu sync.Mutex
	tsBytes := 0
	sub, unsub := st.Subscribe()
	defer unsub()
	go func() {
		for c := range sub {
			tsMu.Lock()
			tsBytes += len(c)
			tsMu.Unlock()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := st.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * time.Second)
	segsBefore := countSegments(dir)
	oldPID := st.PID()
	killedAt := time.Now()
	st.muxMu.Lock()
	_ = st.mux.cmd.Process.Kill()
	st.muxMu.Unlock()

	waitForT(t, 5*time.Second, func() bool { return st.MuxerRestarts() == 1 && st.PID() != 0 })
	t.Logf("muxer restarted after %v (pid %d → %d)", time.Since(killedAt), oldPID, st.PID())
	if st.PID() == oldPID || !st.Alive() {
		t.Fatalf("muxer not replaced / station died")
	}
	tsMu.Lock()
	tsAtRestart := tsBytes
	tsMu.Unlock()

	time.Sleep(10 * time.Second)
	segsAfter := countSegments(dir)
	if segsAfter < segsBefore+3 {
		t.Errorf("HLS stalled after restart: %d → %d segments", segsBefore, segsAfter)
	}
	tsMu.Lock()
	grown := tsBytes - tsAtRestart
	tsMu.Unlock()
	if grown < 200_000 {
		t.Errorf("TS client got only %d bytes after the restart", grown)
	}
	// Same timeline after the restart (-copyts): the newest segment's pts
	// sits where the wall clock says, not back at zero.
	pl, _ := readShared(filepath.Join(dir, "index.m3u8"))
	if !strings.Contains(string(pl), "#EXT-X-DISCONTINUITY") {
		t.Error("restarted playlist should carry a discontinuity marker")
	}
	names, _ := parsePlaylist(string(pl))
	if len(names) < 2 {
		t.Fatalf("restarted playlist: %s", pl)
	}
	stNew, _ := probeStartTime(t, filepath.Join(dir, names[len(names)-2]))
	elapsed := time.Since(st.Epoch()).Seconds()
	if stNew < elapsed-8 || stNew > elapsed+5 {
		t.Errorf("after restart pts=%.2f but %.2fs of channel time elapsed", stNew, elapsed)
	}
	logs, _ := os.ReadFile(filepath.Join(dir, "mux.log"))
	if !strings.Contains(string(logs), "muxer restart") {
		t.Error("mux.log should record the restart")
	}
}

func countSegments(dir string) int {
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if segRe.MatchString(e.Name()) {
			n++
		}
	}
	return n
}

func waitForT(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

// probeVideoEnd returns the segment's video start+duration (pts seconds).
func probeVideoEnd(path string) (float64, bool) {
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=start_time,duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		return 0, false
	}
	f := strings.Split(strings.TrimSpace(strings.Split(string(out), "\n")[0]), ",")
	if len(f) < 2 {
		return 0, false
	}
	st, err1 := strconv.ParseFloat(strings.TrimSpace(f[0]), 64)
	d, err2 := strconv.ParseFloat(strings.TrimSpace(f[1]), 64)
	return st + d, err1 == nil && err2 == nil
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
