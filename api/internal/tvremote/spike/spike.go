// Package spike is the Cloudflare Stream spike of Epic 32 story 32.6
// (AC7): with a real account it creates a live input, pushes a test
// pattern with ffmpeg over RTMPS (or SRT), and measures and checks what
// the design assumes but Cloudflare does not document:
//
//	(a) a token whose sub is the in-progress video id plays the broadcast
//	    (and whether one whose sub is the input id does too)
//	(b) whether exp is enforced per segment (a playlist fetched after the
//	    token expired, and a segment)
//	(c) time to live and time to playable (and LL-HLS with -low-latency);
//	    glass-to-glass with the burned-in clock (-clock) by eye
//	(d) a reconnect within timeoutSeconds keeps the same video id
//	(e) deleting the recording right after the broadcast frees storage
//	(f) the account's concurrent live input limit (-limit-probe N)
//	(g) whether a live output reports its destination's state, and whether
//	    it survives a reconnect (-output-url)
//
// It cleans up after itself (inputs, recordings, its temporary signing
// key) unless -keep. Run it with `maktaba-api cloudflare spike`; see
// tools/tv/cf-spike.md. It never prints the token or a stream key.
package spike

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote/cf"
)

// Pusher starts a broadcast to an ingest endpoint; stop ends it.
type Pusher interface {
	Start(ctx context.Context, target string) (stop func() error, err error)
}

// FFmpegPusher pushes a generated test pattern (or Source, an MPEG-TS
// file, looped) with ffmpeg, encoded like a Maktaba channel: H.264 high,
// 2 s closed GOP, no B-frames, CBR-like 6 Mb/s, AAC.
type FFmpegPusher struct {
	Bin    string
	Source string
	// Clock burns the wall clock (UTC, ms) into the picture, to read
	// glass-to-glass latency off a player by eye.
	Clock bool
	// Log receives ffmpeg's stderr (the stream key is in its argv only).
	Log io.Writer
}

// Args is ffmpeg's argument list for target (exported for tests).
func (p FFmpegPusher) Args(target string) []string {
	var in []string
	if p.Source != "" {
		in = []string{"-re", "-stream_loop", "-1", "-i", p.Source}
	} else {
		in = []string{"-re", "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30",
			"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000"}
	}
	args := append([]string{"-hide_banner", "-loglevel", "warning"}, in...)
	if p.Clock {
		args = append(args, "-vf",
			`drawtext=text='%{localtime\:%H\\\:%M\\\:%S} %{eif\:mod(t*1000\,1000)\:d\:3}':x=40:y=40:fontsize=64:fontcolor=white:box=1:boxcolor=black@0.6`)
	}
	args = append(args,
		"-map", "0:v:0", "-map", map[bool]string{true: "0:a:0", false: "1:a:0"}[p.Source != ""],
		"-c:v", "libx264", "-profile:v", "high", "-preset", "veryfast", "-tune", "zerolatency",
		"-b:v", "6000k", "-maxrate", "6000k", "-bufsize", "12000k",
		"-g", "60", "-keyint_min", "60", "-sc_threshold", "0", "-bf", "0", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k", "-ar", "48000")
	if strings.HasPrefix(target, "srt://") {
		return append(args, "-f", "mpegts", target)
	}
	return append(args, "-f", "flv", "-flvflags", "no_duration_filesize", target)
}

// Start implements Pusher.
func (p FFmpegPusher) Start(ctx context.Context, target string) (func() error, error) {
	bin := p.Bin
	if bin == "" {
		bin = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, bin, p.Args(target)...) //nolint:gosec // operator-run spike
	if p.Log != nil {
		cmd.Stderr = p.Log
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var once sync.Once
	return func() error {
		once.Do(func() {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		})
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
		return nil
	}, nil
}

// Options drive one spike run.
type Options struct {
	Client       *cf.Client
	CustomerCode string // learned from the broadcast's video when empty
	Ingest       string // rtmps (default) | srt
	Duration     time.Duration
	// Drops are ingest drops to force mid-broadcast (each: stop the push,
	// wait, push again); with TimeoutSeconds 30, 10 s should keep the
	// video id and 60 s should not.
	Drops          []time.Duration
	TimeoutSeconds int
	LowLatency     bool
	// ExpTTL is the life of the token of check (b) (default 40 s).
	ExpTTL     time.Duration
	LimitProbe int
	OutputURL  string
	OutputKey  string
	Keep       bool

	Pusher      Pusher
	HTTP        *http.Client
	Poll        time.Duration // default 1 s
	LiveTimeout time.Duration // default 90 s
	StorageWait time.Duration // default 5 min
	Now         func() time.Time
	Sleep       func(ctx context.Context, d time.Duration) error
	// Log receives progress lines.
	Log io.Writer
}

// Fetch is one HTTP check's outcome.
type Fetch struct {
	Status int    `json:"status"`
	Error  string `json:"error,omitempty"`
}

// OK reports a 2xx.
func (f Fetch) OK() bool { return f.Status >= 200 && f.Status < 300 }

// DropResult is check (d) for one drop.
type DropResult struct {
	Drop          string  `json:"drop"`
	VideoBefore   string  `json:"video_before"`
	VideoAfter    string  `json:"video_after"`
	SameVideo     bool    `json:"same_video"`
	LiveAgainSecs float64 `json:"live_again_seconds"`
}

// Report is the spike's findings (printed as JSON).
type Report struct {
	StartedAt    time.Time `json:"started_at"`
	InputUID     string    `json:"input_uid"`
	VideoUID     string    `json:"video_uid"`
	CustomerCode string    `json:"customer_code"`
	Ingest       string    `json:"ingest"`
	LowLatency   bool      `json:"low_latency"`

	TimeToLiveSecs     float64 `json:"time_to_live_seconds"`
	TimeToPlayableSecs float64 `json:"time_to_playable_seconds"`
	// PDTLagSecs is now minus the newest segment's EXT-X-PROGRAM-DATE-TIME
	// (a lower bound of the latency; nil when the playlist has none).
	PDTLagSecs *float64 `json:"program_date_time_lag_seconds"`

	TokenVideoSub Fetch `json:"token_sub_video_manifest"`
	TokenInputSub Fetch `json:"token_sub_input_manifest"`
	Unsigned      Fetch `json:"unsigned_manifest"`

	ExpTTLSecs       float64 `json:"exp_ttl_seconds"`
	PlaylistAfterExp Fetch   `json:"playlist_after_exp"`
	SegmentAfterExp  Fetch   `json:"segment_after_exp"`
	// ExpPerSegment: true when a segment fetched after exp is refused.
	ExpPerSegment *bool `json:"exp_enforced_per_segment"`

	Drops []DropResult `json:"drops"`

	RecordingUID        string   `json:"recording_uid"`
	StorageBefore       float64  `json:"storage_minutes_before"`
	StorageAfterLive    float64  `json:"storage_minutes_after_broadcast"`
	StorageAfterDelete  float64  `json:"storage_minutes_after_delete"`
	StorageFreed        *bool    `json:"storage_freed"`
	StorageFreedAfter   *float64 `json:"storage_freed_after_seconds"`
	InputLimitCreated   int      `json:"input_limit_created"`
	InputLimitError     string   `json:"input_limit_error,omitempty"`
	OutputAnswer        string   `json:"output_answer,omitempty"`
	OutputAfterRecon    string   `json:"output_after_reconnect,omitempty"`
	OutputSurvived      *bool    `json:"output_survived_reconnect"`
	OutputReportsStatus *bool    `json:"output_reports_status"`

	Notes  []string `json:"notes"`
	Errors []string `json:"errors"`
}

type run struct {
	o     Options
	rep   *Report
	key   cf.SigningKey
	start time.Time
}

func (r *run) now() time.Time {
	if r.o.Now != nil {
		return r.o.Now()
	}
	return time.Now()
}

func (r *run) sleep(ctx context.Context, d time.Duration) error {
	if r.o.Sleep != nil {
		return r.o.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (r *run) logf(format string, a ...any) {
	if r.o.Log != nil {
		fmt.Fprintf(r.o.Log, "spike: "+format+"\n", a...)
	}
}

func (r *run) fail(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	r.rep.Errors = append(r.rep.Errors, msg)
	r.logf("error: %s", msg)
}

func (r *run) httpc() *http.Client {
	if r.o.HTTP != nil {
		return r.o.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (r *run) get(ctx context.Context, u string) (Fetch, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Fetch{Error: err.Error()}, ""
	}
	resp, err := r.httpc().Do(req)
	if err != nil {
		return Fetch{Error: err.Error()}, ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return Fetch{Status: resp.StatusCode}, string(b)
}

// Run executes the spike. It returns the report even on error.
func Run(ctx context.Context, o Options) (Report, error) {
	if o.Client == nil {
		return Report{}, errors.New("spike: no Cloudflare client")
	}
	if o.Pusher == nil {
		o.Pusher = FFmpegPusher{}
	}
	if o.Duration <= 0 {
		o.Duration = 60 * time.Second
	}
	if o.Poll <= 0 {
		o.Poll = time.Second
	}
	if o.LiveTimeout <= 0 {
		o.LiveTimeout = 90 * time.Second
	}
	if o.StorageWait <= 0 {
		o.StorageWait = 5 * time.Minute
	}
	if o.ExpTTL <= 0 {
		o.ExpTTL = 40 * time.Second
	}
	if o.TimeoutSeconds <= 0 {
		o.TimeoutSeconds = 30
	}
	if o.Ingest == "" {
		o.Ingest = "rtmps"
	}
	rep := &Report{Ingest: o.Ingest, LowLatency: o.LowLatency, CustomerCode: o.CustomerCode,
		Notes: []string{}, Errors: []string{}, Drops: []DropResult{}}
	r := &run{o: o, rep: rep}
	rep.StartedAt = r.now().UTC()
	err := r.main(ctx)
	if err != nil {
		r.fail("%v", err)
	}
	return *rep, err
}

func (r *run) main(ctx context.Context) error {
	cl, rep := r.o.Client, r.rep
	if u, err := cl.StorageUsage(ctx); err == nil {
		rep.StorageBefore = u.TotalStorageMinutes
	} else {
		r.fail("storage usage: %v", err)
	}
	key, err := cl.CreateSigningKey(ctx)
	if err != nil {
		return fmt.Errorf("create a temporary signing key: %w", err)
	}
	r.key = key
	defer func() {
		if r.o.Keep {
			rep.Notes = append(rep.Notes, "kept signing key "+key.ID)
			return
		}
		if err := cl.DeleteSigningKey(context.WithoutCancel(ctx), key.ID); err != nil {
			r.fail("delete the temporary signing key %s: %v", key.ID, err)
		}
	}()

	timeout := r.o.TimeoutSeconds
	in, err := cl.CreateLiveInput(ctx, cf.CreateLiveInput{
		Meta:             map[string]any{"name": cf.LiveInputPrefix + "spike-" + r.now().UTC().Format("20060102-150405")},
		Recording:        &cf.Recording{Mode: "automatic", RequireSignedURLs: true, TimeoutSeconds: timeout},
		PreferLowLatency: r.o.LowLatency,
	})
	if err != nil {
		return fmt.Errorf("create a live input: %w", err)
	}
	rep.InputUID = in.UID
	r.logf("live input %s created", in.UID)
	defer func() {
		if r.o.Keep {
			rep.Notes = append(rep.Notes, "kept live input "+in.UID)
			return
		}
		if err := cl.DeleteLiveInput(context.WithoutCancel(ctx), in.UID); err != nil {
			r.fail("delete live input %s: %v", in.UID, err)
		}
	}()
	target, err := ingestTarget(in, r.o.Ingest)
	if err != nil {
		return err
	}

	if r.o.OutputURL != "" {
		if out, err := cl.CreateOutput(ctx, in.UID, r.o.OutputURL, r.o.OutputKey, true); err != nil {
			r.fail("create live output: %v", err)
		} else {
			rep.OutputAnswer = redactKeys(string(out.Raw))
			has := strings.Contains(string(out.Raw), `"status"`) || strings.Contains(string(out.Raw), `"state"`)
			rep.OutputReportsStatus = &has
		}
	}

	r.start = r.now()
	stop, err := r.o.Pusher.Start(ctx, target)
	if err != nil {
		return err
	}
	stopped := false
	defer func() {
		if !stopped {
			_ = stop()
		}
	}()
	video, err := r.waitLive(ctx, in.UID, "")
	if err != nil {
		return err
	}
	rep.VideoUID = video
	rep.TimeToLiveSecs = r.now().Sub(r.start).Seconds()
	r.logf("live after %.1f s, video %s", rep.TimeToLiveSecs, video)
	if rep.CustomerCode == "" {
		return errors.New("no customer code: pass -customer-code (Stream, Live inputs, the HLS manifest URL)")
	}

	r.checkTokens(ctx, in.UID, video)
	r.checkExp(ctx, video)

	end := r.start.Add(r.o.Duration)
	for _, d := range r.o.Drops {
		_ = stop()
		r.logf("ingest dropped for %s", d)
		if err := r.sleep(ctx, d); err != nil {
			return err
		}
		t0 := r.now()
		if stop, err = r.o.Pusher.Start(ctx, target); err != nil {
			return err
		}
		after, err := r.waitLive(ctx, in.UID, "")
		dr := DropResult{Drop: d.String(), VideoBefore: video, VideoAfter: after, SameVideo: after == video,
			LiveAgainSecs: r.now().Sub(t0).Seconds()}
		rep.Drops = append(rep.Drops, dr)
		if err != nil {
			r.fail("after a %s drop: %v", d, err)
			continue
		}
		r.logf("after a %s drop: video %s (same: %v)", d, after, dr.SameVideo)
		video = after
	}
	if r.o.OutputURL != "" {
		if outs, err := cl.ListOutputs(ctx, in.UID); err != nil {
			r.fail("list live outputs: %v", err)
		} else {
			survived := len(outs) > 0
			rep.OutputSurvived = &survived
			if survived {
				rep.OutputAfterRecon = redactKeys(string(outs[0].Raw))
			}
		}
	}
	if rest := end.Sub(r.now()); rest > 0 {
		if err := r.sleep(ctx, rest); err != nil {
			return err
		}
	}
	_ = stop()
	stopped = true
	r.logf("broadcast stopped")

	r.checkRecording(ctx, in.UID)
	if r.o.LimitProbe > 0 {
		r.probeLimit(ctx)
	}
	return nil
}

func ingestTarget(in cf.LiveInput, kind string) (string, error) {
	if kind == "srt" {
		if in.SRT == nil || in.SRT.URL == "" {
			return "", errors.New("the live input has no SRT endpoint")
		}
		return in.SRT.URL + "?streamid=" + url.QueryEscape(in.SRT.StreamID) +
			"&passphrase=" + url.QueryEscape(in.SRT.Passphrase) + "&mode=caller", nil
	}
	if in.RTMPS == nil || in.RTMPS.URL == "" || in.RTMPS.StreamKey == "" {
		return "", errors.New("the live input has no RTMPS endpoint")
	}
	return strings.TrimRight(in.RTMPS.URL, "/") + "/" + in.RTMPS.StreamKey, nil
}

var streamKeyRe = regexp.MustCompile(`"(streamKey|passphrase)"\s*:\s*"[^"]*"`)

// redactKeys masks stream keys and passphrases in a raw answer.
func redactKeys(s string) string {
	return streamKeyRe.ReplaceAllString(s, `"$1":"<redacted>"`)
}

// waitLive polls the lifecycle until the input is live with a video id
// other than not (when not is set). It learns the customer code from the
// input's videos when none was given.
func (r *run) waitLive(ctx context.Context, input, not string) (string, error) {
	deadline := r.now().Add(r.o.LiveTimeout)
	for r.now().Before(deadline) {
		if r.rep.CustomerCode == "" {
			if vs, err := r.o.Client.InputVideos(ctx, input); err == nil {
				for _, v := range vs {
					if c := cf.CustomerCode(v.Playback.HLS); c != "" {
						r.rep.CustomerCode = c
						r.rep.Notes = append(r.rep.Notes, "customer code learned from the broadcast's video")
						break
					}
				}
			}
		}
		if r.rep.CustomerCode != "" {
			lc, err := r.o.Client.Lifecycle(ctx, r.rep.CustomerCode, input)
			if err == nil && lc.Live && lc.VideoUID != "" && lc.VideoUID != not {
				return lc.VideoUID, nil
			}
		}
		if err := r.sleep(ctx, r.o.Poll); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("not live within %s", r.o.LiveTimeout)
}

func (r *run) token(sub string, ttl time.Duration) string {
	t, err := cf.SignToken(r.key, sub, ttl, r.now(), nil)
	if err != nil {
		r.fail("sign a token: %v", err)
	}
	return t
}

// checkTokens is (a) plus time to playable and the PDT lag.
func (r *run) checkTokens(ctx context.Context, input, video string) {
	cl, rep := r.o.Client, r.rep
	tok := r.token(video, 10*time.Minute)
	deadline := r.now().Add(r.o.LiveTimeout)
	for {
		f, body := r.get(ctx, cl.ManifestURL(rep.CustomerCode, tok))
		rep.TokenVideoSub = f
		if f.OK() {
			if seg, pdt := r.firstSegment(ctx, cl.ManifestURL(rep.CustomerCode, tok), body); seg != "" {
				rep.TimeToPlayableSecs = r.now().Sub(r.start).Seconds()
				r.logf("playable after %.1f s; watch it (token valid 10 min): %s", rep.TimeToPlayableSecs, cl.ManifestURL(rep.CustomerCode, tok))
				if pdt != nil {
					lag := r.now().Sub(*pdt).Seconds()
					rep.PDTLagSecs = &lag
				}
				break
			}
		}
		if !r.now().Before(deadline) {
			r.fail("the manifest with a video-id token did not become playable (last status %d)", f.Status)
			break
		}
		if r.sleep(ctx, r.o.Poll) != nil {
			return
		}
	}
	rep.TokenInputSub, _ = r.get(ctx, cl.ManifestURL(rep.CustomerCode, r.token(input, 10*time.Minute)))
	rep.Unsigned, _ = r.get(ctx, cl.ManifestURL(rep.CustomerCode, video))
	r.logf("manifest: video-id token %d, input-id token %d, unsigned %d",
		rep.TokenVideoSub.Status, rep.TokenInputSub.Status, rep.Unsigned.Status)
}

// firstSegment follows a master playlist to its first variant and returns
// the newest segment's URL and its program date time.
func (r *run) firstSegment(ctx context.Context, manifestURL, master string) (string, *time.Time) {
	variant := firstURI(master)
	if variant == "" {
		return "", nil
	}
	vu := resolve(manifestURL, variant)
	body := master
	if strings.Contains(master, "#EXT-X-STREAM-INF") {
		f, b := r.get(ctx, vu)
		if !f.OK() {
			return "", nil
		}
		body = b
	} else {
		vu = manifestURL
	}
	seg, pdt := lastSegment(body)
	if seg == "" {
		return "", nil
	}
	return resolve(vu, seg), pdt
}

func firstURI(playlist string) string {
	sc := bufio.NewScanner(strings.NewReader(playlist))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l != "" && !strings.HasPrefix(l, "#") {
			return l
		}
	}
	return ""
}

func lastSegment(playlist string) (string, *time.Time) {
	var seg string
	var pdt *time.Time
	var pending *time.Time
	sc := bufio.NewScanner(strings.NewReader(playlist))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(l, "#EXT-X-PROGRAM-DATE-TIME:"):
			if t, err := time.Parse(time.RFC3339Nano, strings.TrimPrefix(l, "#EXT-X-PROGRAM-DATE-TIME:")); err == nil {
				pending = &t
			}
		case l != "" && !strings.HasPrefix(l, "#"):
			seg, pdt, pending = l, pending, nil
		}
	}
	return seg, pdt
}

func resolve(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	u, err := b.Parse(ref)
	if err != nil {
		return ref
	}
	return u.String()
}

// checkExp is (b): a short token, then the playlist and a segment after
// it expired.
func (r *run) checkExp(ctx context.Context, video string) {
	cl, rep := r.o.Client, r.rep
	rep.ExpTTLSecs = r.o.ExpTTL.Seconds()
	tok := r.token(video, r.o.ExpTTL)
	mu := cl.ManifestURL(rep.CustomerCode, tok)
	f, body := r.get(ctx, mu)
	if !f.OK() {
		r.fail("exp check: manifest %d", f.Status)
		return
	}
	variant := mu
	if strings.Contains(body, "#EXT-X-STREAM-INF") {
		variant = resolve(mu, firstURI(body))
	}
	r.logf("waiting %s for the token to expire", r.o.ExpTTL+5*time.Second)
	if r.sleep(ctx, r.o.ExpTTL+5*time.Second) != nil {
		return
	}
	pf, pbody := r.get(ctx, variant)
	rep.PlaylistAfterExp = pf
	seg := ""
	if pf.OK() {
		seg, _ = lastSegment(pbody)
	}
	if seg == "" {
		// the playlist was refused: try a segment of a fresh playlist
		// with the expired token's path
		fresh := r.token(video, 10*time.Minute)
		ff, fbody := r.get(ctx, strings.Replace(variant, tok, fresh, 1))
		if ff.OK() {
			if s, _ := lastSegment(fbody); s != "" {
				seg = strings.Replace(resolve(strings.Replace(variant, tok, fresh, 1), s), fresh, tok, 1)
			}
		}
	} else {
		seg = resolve(variant, seg)
	}
	if seg == "" {
		rep.Notes = append(rep.Notes, "exp check: no segment URL to try after exp")
		return
	}
	sf, _ := r.get(ctx, seg)
	rep.SegmentAfterExp = sf
	enforced := !sf.OK()
	rep.ExpPerSegment = &enforced
	r.logf("after exp: playlist %d, segment %d", pf.Status, sf.Status)
}

// checkRecording is (e).
func (r *run) checkRecording(ctx context.Context, input string) {
	cl, rep := r.o.Client, r.rep
	deadline := r.now().Add(r.o.StorageWait)
	var rec string
	for r.now().Before(deadline) && rec == "" {
		if vs, err := cl.InputVideos(ctx, input); err == nil {
			for _, v := range vs {
				if v.Status.State == "ready" {
					rec = v.UID
					break
				}
			}
		}
		if rec == "" && r.sleep(ctx, r.o.Poll*5) != nil {
			return
		}
	}
	if rec == "" {
		r.fail("no ready recording within %s", r.o.StorageWait)
		return
	}
	rep.RecordingUID = rec
	if u, err := cl.StorageUsage(ctx); err == nil {
		rep.StorageAfterLive = u.TotalStorageMinutes
	}
	if r.o.Keep {
		rep.Notes = append(rep.Notes, "kept recording "+rec)
		return
	}
	t0 := r.now()
	if err := cl.DeleteVideo(ctx, rec); err != nil {
		r.fail("delete the recording: %v", err)
		return
	}
	freed := false
	for r.now().Sub(t0) < r.o.StorageWait {
		if u, err := cl.StorageUsage(ctx); err == nil {
			rep.StorageAfterDelete = u.TotalStorageMinutes
			if u.TotalStorageMinutes < rep.StorageAfterLive || u.TotalStorageMinutes <= rep.StorageBefore {
				freed = true
				secs := r.now().Sub(t0).Seconds()
				rep.StorageFreedAfter = &secs
				break
			}
		}
		if r.sleep(ctx, r.o.Poll*5) != nil {
			break
		}
	}
	rep.StorageFreed = &freed
	r.logf("recording deleted; storage freed: %v", freed)
}

// probeLimit is (f): create inputs until refused or LimitProbe, then
// delete them.
func (r *run) probeLimit(ctx context.Context) {
	cl, rep := r.o.Client, r.rep
	var made []string
	for i := 0; i < r.o.LimitProbe; i++ {
		off := false
		in, err := cl.CreateLiveInput(ctx, cf.CreateLiveInput{
			Meta: map[string]any{"name": fmt.Sprintf("%sspike-limit-%d", cf.LiveInputPrefix, i)}, Enabled: &off})
		if err != nil {
			rep.InputLimitError = err.Error()
			break
		}
		made = append(made, in.UID)
	}
	rep.InputLimitCreated = len(made)
	for _, id := range made {
		if err := cl.DeleteLiveInput(context.WithoutCancel(ctx), id); err != nil {
			r.fail("delete probe input %s: %v", id, err)
		}
	}
	r.logf("input limit probe: created %d (%s)", len(made), rep.InputLimitError)
}

// Env reads the spike's credentials: MAKTABA_CF_ACCOUNT_ID and
// MAKTABA_CF_API_TOKEN (or MAKTABA_CF_API_TOKEN_FILE). The token is never
// a flag, so it stays out of the process list and shell history.
func Env(getenv func(string) string) (account, token string, err error) {
	account = strings.TrimSpace(getenv("MAKTABA_CF_ACCOUNT_ID"))
	token = strings.TrimSpace(getenv("MAKTABA_CF_API_TOKEN"))
	if token == "" {
		if p := strings.TrimSpace(getenv("MAKTABA_CF_API_TOKEN_FILE")); p != "" {
			b, rerr := os.ReadFile(p) //nolint:gosec // operator-given path
			if rerr != nil {
				return "", "", fmt.Errorf("read MAKTABA_CF_API_TOKEN_FILE: %w", rerr)
			}
			token = strings.TrimSpace(string(b))
		}
	}
	if account == "" || token == "" {
		return "", "", errors.New("set MAKTABA_CF_ACCOUNT_ID and MAKTABA_CF_API_TOKEN (or MAKTABA_CF_API_TOKEN_FILE)")
	}
	return account, token, nil
}
