// Package logship keeps a cloud process's log lines on its node and forwards them to the
// witness W (Epic 53 story 53.17 §2), which keeps every node's copy and runs the log index
// over them, so the whole cloud can be searched from one place while A or B is down.
//
//   - Tee is the logger's second writer: each line goes to this node's keeper (the same
//     keeper a home runs, shared/logkeeper/go: segments, 14 days, a size cap) and, when a
//     Forwarder is set, to W.
//   - Forwarder sends warn and error lines at once (within a second) and the rest every 10
//     seconds, over the private network, numbered: what W has acknowledged is dropped, what
//     it has not is sent again, so a network drop leaves no gap and no line twice. While W
//     cannot be reached the queue is bounded (info goes first), and the lag is a gauge.
//   - Receiver is W's end: it keeps each node's lines under the node's name and answers how
//     far it has them.
package logship

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	logkeeper "github.com/Hamza-Labs-Core/Maktaba/shared/logkeeper/go"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/obs"
)

// Defaults of the cloud's log path (Epic 50 §4.5).
const (
	Retention     = 14 * 24 * time.Hour
	MaxSegments   = 5 << 30
	BatchEvery    = 10 * time.Second
	UrgentWithin  = 500 * time.Millisecond
	DefaultQueue  = 50_000
	maxBatchLines = 5_000
	IngestPath    = "/v1/logs/ingest"
)

// NewKeeper is a node's keeper of the cloud's lines (root: /var/lib/maktaba-cloud/logs).
func NewKeeper(root, node string) *logkeeper.Keeper {
	return logkeeper.New(logkeeper.Config{Root: root, Node: node, Retention: Retention, MaxSegments: MaxSegments})
}

// Tee splits what the logger writes into lines and keeps and forwards each.
type Tee struct {
	Keeper    *logkeeper.Keeper
	Forwarder *Forwarder
	Origin    logkeeper.Origin

	mu  sync.Mutex
	buf []byte
}

// Write implements io.Writer.
func (t *Tee) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(t.buf[:i])
		if len(line) > 0 {
			if t.Keeper != nil {
				t.Keeper.Ingest(t.Origin, line)
			}
			if t.Forwarder != nil {
				t.Forwarder.Add(line)
			}
		}
		t.buf = t.buf[i+1:]
	}
	if len(t.buf) > 1<<20 { // a line with no end: kept as it is
		t.buf = t.buf[:0]
	}
	return len(p), nil
}

type item struct {
	Seq  uint64          `json:"seq"`
	Line json.RawMessage `json:"line"`
	at   time.Time
	warn bool
}

// Batch is what a Forwarder posts.
type Batch struct {
	Boot    string `json:"boot"`
	Node    string `json:"node"`
	Service string `json:"service"`
	Lines   []item `json:"lines"`
}

// Forwarder sends a process's lines to W.
type Forwarder struct {
	URL, Token    string
	Node, Service string
	Client        *http.Client
	Max           int
	Now           func() time.Time
	Metrics       *obs.Series

	mu    sync.Mutex
	boot  string
	next  uint64
	queue []item
	kick  chan struct{}
	once  sync.Once
}

func (f *Forwarder) init() {
	f.once.Do(func() {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		f.boot = hex.EncodeToString(b)
		f.kick = make(chan struct{}, 1)
		if f.Max <= 0 {
			f.Max = DefaultQueue
		}
		if f.Now == nil {
			f.Now = time.Now
		}
		if f.Metrics == nil {
			f.Metrics = obs.Default
		}
	})
}

var warnRe = regexp.MustCompile(`"level":"(warn|warning|error|fatal)"`)

// Add queues one line; a warn or error line is sent at once.
func (f *Forwarder) Add(line []byte) {
	f.init()
	warn := warnRe.Match(line)
	f.mu.Lock()
	f.next++
	f.queue = append(f.queue, item{Seq: f.next, Line: append(json.RawMessage(nil), line...), at: f.Now(), warn: warn})
	dropped := 0
	for len(f.queue) > f.Max {
		// the oldest info line goes first; with none left, the oldest line
		i := 0
		for j, it := range f.queue {
			if !it.warn {
				i = j
				break
			}
		}
		f.queue = append(f.queue[:i], f.queue[i+1:]...)
		dropped++
	}
	f.mu.Unlock()
	if dropped > 0 {
		f.Metrics.LogDropped(f.Service, int64(dropped))
	}
	if warn {
		select {
		case f.kick <- struct{}{}:
		default:
		}
	}
}

// Queued is how many lines wait for W.
func (f *Forwarder) Queued() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queue)
}

// Run sends until ctx ends, then once more (a stopping process's last lines).
func (f *Forwarder) Run(ctx context.Context) {
	f.init()
	t := time.NewTicker(BatchEvery)
	defer t.Stop()
	lag := time.NewTicker(time.Second)
	defer lag.Stop()
	for {
		select {
		case <-ctx.Done():
			sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = f.Flush(sctx)
			cancel()
			return
		case <-f.kick:
			// a warn line: wait a moment for the lines around it, then send
			select {
			case <-time.After(UrgentWithin):
			case <-ctx.Done():
			}
			_ = f.Flush(ctx)
		case <-t.C:
			_ = f.Flush(ctx)
		case <-lag.C:
			f.reportLag()
		}
	}
}

func (f *Forwarder) reportLag() {
	f.mu.Lock()
	var d time.Duration
	if len(f.queue) > 0 {
		d = f.Now().Sub(f.queue[0].at)
	}
	f.mu.Unlock()
	f.Metrics.ForwardLag(f.Service, d)
}

// Flush sends everything queued, batch by batch, until the queue is empty or W does not
// take a batch.
func (f *Forwarder) Flush(ctx context.Context) error {
	f.init()
	for {
		f.mu.Lock()
		n := min(len(f.queue), maxBatchLines)
		batch := Batch{Boot: f.boot, Node: f.Node, Service: f.Service, Lines: append([]item(nil), f.queue[:n]...)}
		f.mu.Unlock()
		if n == 0 {
			f.reportLag()
			return nil
		}
		acked, err := f.post(ctx, batch)
		f.mu.Lock()
		i := 0
		for i < len(f.queue) && f.queue[i].Seq <= acked {
			i++
		}
		f.queue = f.queue[i:]
		f.mu.Unlock()
		f.reportLag()
		if err != nil {
			return err
		}
		if acked < batch.Lines[len(batch.Lines)-1].Seq {
			return errors.New("logship: W took part of a batch")
		}
	}
}

func (f *Forwarder) post(ctx context.Context, b Batch) (uint64, error) {
	body, err := json.Marshal(b)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(f.URL, "/")+IngestPath, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.Token)
	cl := f.Client
	if cl == nil {
		cl = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("logship: W answered %d", resp.StatusCode)
	}
	var got struct {
		Acked uint64 `json:"acked"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&got); err != nil {
		return 0, err
	}
	return got.Acked, nil
}

// Receiver is W's ingest: each node's lines are kept under the node's name, once.
type Receiver struct {
	Keeper *logkeeper.Keeper
	Token  string
	// State is where how far each process's lines were taken is kept (a restart of W must
	// not take a line twice).
	State string

	mu   sync.Mutex
	last map[string]taken
}

// taken is how far one process's lines were taken, and when (a process's entry is forgotten
// 30 days after its last line: a restart is a new process).
type taken struct {
	Seq uint64    `json:"seq"`
	At  time.Time `json:"at"`
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (rv *Receiver) load() {
	if rv.last != nil {
		return
	}
	rv.last = map[string]taken{}
	if rv.State != "" {
		if b, err := os.ReadFile(rv.State); err == nil {
			_ = json.Unmarshal(b, &rv.last)
		}
	}
}

func (rv *Receiver) save() {
	if rv.State == "" {
		return
	}
	for k, v := range rv.last {
		if time.Since(v.At) > 30*24*time.Hour {
			delete(rv.last, k)
		}
	}
	b, _ := json.Marshal(rv.last)
	tmp := rv.State + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, rv.State)
	}
}

// ServeHTTP takes one batch: POST /v1/logs/ingest, the bearer token.
func (rv *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if rv.Token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(rv.Token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var b Batch
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<20)).Decode(&b); err != nil {
		http.Error(w, "bad batch", http.StatusBadRequest)
		return
	}
	if !nameRe.MatchString(b.Node) || !nameRe.MatchString(b.Service) || !nameRe.MatchString(b.Boot) {
		http.Error(w, "bad names", http.StatusBadRequest)
		return
	}
	key := b.Node + "|" + b.Service + "|" + b.Boot
	rv.mu.Lock()
	defer rv.mu.Unlock()
	rv.load()
	last := rv.last[key].Seq
	origin := logkeeper.Origin{Service: b.Service, Instance: b.Node}
	for _, it := range b.Lines {
		if it.Seq <= last {
			continue // taken before: an answer that did not arrive
		}
		rv.Keeper.Ingest(origin, it.Line)
		last = it.Seq
	}
	rv.last[key] = taken{Seq: last, At: time.Now().UTC()}
	rv.save()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]uint64{"acked": last})
}

// StatePath is the receiver's state file under a keeper's root.
func StatePath(root string) string { return filepath.Join(root, "ingest-state.json") }
