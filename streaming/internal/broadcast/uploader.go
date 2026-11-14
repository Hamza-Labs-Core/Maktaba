package broadcast

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Creds are a channel's ingest token and where it goes (broadcast_ingest_tokens).
type Creds struct {
	Token     string
	IngestURL string
	// Channel is the edge's opaque channel id.
	Channel   string
	ExpiresAt time.Time
}

// Segment is one finished segment of one rung, as its playlist lists it.
type Segment struct {
	Rung          string
	Seq           int64
	Path          string
	Duration      float64
	Discontinuity bool
	PDT           time.Time
}

// Outcome is what became of a segment.
type Outcome int

const (
	// Sent: the edge has it (new, a duplicate of a retry, or a slot already past).
	Sent Outcome = iota
	// Skipped: not sent within the retry window (or refused for good); the next segment of
	// its rung carries a discontinuity.
	Skipped
)

// MaxOnDisk is the most segments a rung keeps on disk (§2).
const MaxOnDisk = 8

// Uploader pushes segments to the edge (PUT /v1/ingest/<channel>/<rung>/<seq>.ts).
type Uploader struct {
	HTTP  *http.Client
	Creds func() (Creds, bool)
	Now   func() time.Time
	// Sleep waits d or until ctx ends (tests replace it).
	Sleep func(ctx context.Context, d time.Duration) error
	// Jitter is a number in [0, 1) (tests fix it).
	Jitter func() float64
	// OnRefusal hears an edge refusal code (clock_skew, revoked, broadcast_off, plan_rungs)
	// and a token that expired.
	OnRefusal func(code string)
	// Uploaded counts each segment sent (tests and the state post).
	Uploaded func(rung string, seq int64, bytes int)

	mu   sync.Mutex
	disc map[string]bool
}

func (u *Uploader) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func (u *Uploader) sleep(ctx context.Context, d time.Duration) error {
	if u.Sleep != nil {
		return u.Sleep(ctx, d)
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

func (u *Uploader) jitter() float64 {
	if u.Jitter != nil {
		return u.Jitter()
	}
	return rand.Float64() //nolint:gosec // retry jitter, not a secret
}

func (u *Uploader) refused(code string) {
	if u.OnRefusal != nil {
		u.OnRefusal(code)
	}
}

// markNext flags the rung's next segment as a discontinuity (after a skip).
func (u *Uploader) markNext(rung string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.disc == nil {
		u.disc = map[string]bool{}
	}
	u.disc[rung] = true
}

func (u *Uploader) takeDisc(rung string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	d := u.disc[rung]
	delete(u.disc, rung)
	return d
}

// RetryWindow is how long a segment is retried: two target durations (§2).
const RetryWindow = 2 * SegmentSeconds * time.Second

// Put sends one segment once, retrying with jitter for up to RetryWindow; past it the
// segment is skipped and the rung's next one flagged as a discontinuity.
func (u *Uploader) Put(ctx context.Context, seg Segment) Outcome {
	body, err := os.ReadFile(seg.Path)
	if err != nil {
		u.markNext(seg.Rung)
		return Skipped
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	disc := seg.Discontinuity || u.takeDisc(seg.Rung)
	deadline := u.now().Add(RetryWindow)
	backoff := 250 * time.Millisecond
	for {
		res := u.try(ctx, seg, body, hash, disc)
		switch res {
		case tryDone:
			if u.Uploaded != nil {
				u.Uploaded(seg.Rung, seg.Seq, len(body))
			}
			return Sent
		case tryGiveUp:
			u.markNext(seg.Rung)
			return Skipped
		}
		// half to all of the backoff, so a home's rungs never retry in step
		wait := time.Duration(float64(backoff) * (0.5 + 0.5*u.jitter()))
		if ctx.Err() != nil || !u.now().Add(wait).Before(deadline) {
			u.markNext(seg.Rung)
			return Skipped
		}
		if u.sleep(ctx, wait) != nil {
			u.markNext(seg.Rung)
			return Skipped
		}
		backoff = min(2*backoff, 2*time.Second)
	}
}

type tryResult int

const (
	tryDone tryResult = iota
	tryAgain
	tryGiveUp
)

func (u *Uploader) try(ctx context.Context, seg Segment, body []byte, hash string, disc bool) tryResult {
	cr, ok := u.Creds()
	if !ok {
		return tryAgain
	}
	if !cr.ExpiresAt.IsZero() && !u.now().Before(cr.ExpiresAt) {
		u.refused("tokenExpired")
		return tryGiveUp
	}
	url := fmt.Sprintf("%s/v1/ingest/%s/%s/%d.ts", strings.TrimRight(cr.IngestURL, "/"), cr.Channel, seg.Rung, seg.Seq)
	rctx, cancel := context.WithTimeout(ctx, RetryWindow)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return tryGiveUp
	}
	req.Header.Set("Authorization", "Bearer "+cr.Token)
	req.Header.Set("Content-Type", "video/mp2t")
	req.Header.Set("X-Maktaba-Time", strconv.FormatInt(u.now().UnixMilli(), 10))
	req.Header.Set("X-Maktaba-Body-SHA256", hash)
	req.Header.Set("X-Maktaba-Duration", strconv.FormatFloat(seg.Duration, 'f', 3, 64))
	if !seg.PDT.IsZero() {
		req.Header.Set("X-Maktaba-Segment-Time", strconv.FormatInt(seg.PDT.UnixMilli(), 10))
	}
	if disc {
		req.Header.Set("X-Maktaba-Discontinuity", "1")
	}
	cl := u.HTTP
	if cl == nil {
		cl = http.DefaultClient
	}
	resp, err := cl.Do(req)
	if err != nil {
		return tryAgain
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	code := problemCode(raw)
	switch {
	case resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK:
		return tryDone // new, a duplicate (a retry that had landed), or stale
	case resp.StatusCode == http.StatusConflict:
		return tryDone // the same seq with another body: the edge keeps the first
	case resp.StatusCode == http.StatusRequestEntityTooLarge:
		return tryGiveUp
	case resp.StatusCode == http.StatusForbidden:
		u.refused(code)
		return tryGiveUp
	case resp.StatusCode == http.StatusBadRequest && code == "clock_skew":
		u.refused("clockSkew")
		return tryAgain
	case resp.StatusCode == http.StatusBadRequest:
		return tryGiveUp
	default: // 401 (a token being replaced), 429, 503, 5xx
		return tryAgain
	}
}

func problemCode(raw []byte) string {
	s := string(raw)
	for _, key := range []string{`"error":"`, `"code":"`} {
		if i := strings.Index(s, key); i >= 0 {
			rest := s[i+len(key):]
			if j := strings.IndexByte(rest, '"'); j >= 0 {
				return rest[:j]
			}
		}
	}
	return ""
}

// ParsePlaylist reads an HLS media playlist's segments (EXTINF, the program date time and
// discontinuities) for rung, their files under dir.
func ParsePlaylist(r io.Reader, dir, rung string) []Segment {
	var out []Segment
	var dur float64
	var disc bool
	var pdt time.Time
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "#EXT-X-DISCONTINUITY":
			disc = true
		case strings.HasPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:"):
			pdt, _ = time.Parse("2006-01-02T15:04:05.000Z0700", strings.TrimPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:"))
		case strings.HasPrefix(line, "#EXTINF:"):
			v := strings.TrimPrefix(line, "#EXTINF:")
			v, _, _ = strings.Cut(v, ",")
			dur, _ = strconv.ParseFloat(v, 64)
		case line != "" && !strings.HasPrefix(line, "#"):
			seq, err := strconv.ParseInt(strings.TrimSuffix(filepath.Base(line), ".ts"), 10, 64)
			if err == nil && dur > 0 {
				out = append(out, Segment{Rung: rung, Seq: seq, Path: filepath.Join(dir, filepath.Base(line)),
					Duration: dur, Discontinuity: disc, PDT: pdt})
			}
			dur, disc, pdt = 0, false, time.Time{}
		}
	}
	return out
}

// RungWatcher uploads one rung's segments as its playlist lists them, each once, and
// deletes each file once sent or skipped, keeping at most MaxOnDisk.
type RungWatcher struct {
	Dir  string
	Rung string
	Up   *Uploader
	// Poll is how often the playlist is read (default 500 ms).
	Poll time.Duration

	last int64
}

// Step uploads what is new in the playlist now and prunes the rung's directory.
func (w *RungWatcher) Step(ctx context.Context) {
	f, err := os.Open(filepath.Join(w.Dir, "index.m3u8"))
	if err != nil {
		return
	}
	segs := ParsePlaylist(f, w.Dir, w.Rung)
	_ = f.Close()
	var fresh []Segment
	for _, s := range segs {
		if s.Seq > w.last {
			fresh = append(fresh, s)
		}
	}
	// fallen behind (the edge slow for long): the oldest are skipped, never queued
	if len(fresh) > MaxOnDisk {
		w.Up.markNext(w.Rung)
		fresh = fresh[len(fresh)-MaxOnDisk:]
	}
	for _, s := range fresh {
		if ctx.Err() != nil {
			return
		}
		w.Up.Put(ctx, s)
		w.last = s.Seq
		_ = os.Remove(s.Path)
	}
	w.prune()
}

// prune deletes every segment already handled, and the oldest past MaxOnDisk.
func (w *RungWatcher) prune() {
	ents, err := os.ReadDir(w.Dir)
	if err != nil {
		return
	}
	var seqs []int64
	for _, e := range ents {
		n, err := strconv.ParseInt(strings.TrimSuffix(e.Name(), ".ts"), 10, 64)
		if err != nil || !strings.HasSuffix(e.Name(), ".ts") {
			continue
		}
		if n <= w.last {
			_ = os.Remove(filepath.Join(w.Dir, e.Name()))
			continue
		}
		seqs = append(seqs, n)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for len(seqs) > MaxOnDisk {
		_ = os.Remove(filepath.Join(w.Dir, strconv.FormatInt(seqs[0], 10)+".ts"))
		seqs = seqs[1:]
	}
}

// Run steps until ctx ends.
func (w *RungWatcher) Run(ctx context.Context) {
	poll := w.Poll
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		w.Step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
