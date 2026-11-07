package logship

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	logkeeper "github.com/Hamza-Labs-Core/Maktaba/shared/logkeeper/go"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/obs"
)

// flaky is the private network: down refuses every request; lose delivers the next request
// to W and then loses W's answer (W took the lines, the node does not know it).
type flaky struct {
	mu   sync.Mutex
	down bool
	lose int
	next http.RoundTripper
}

func (f *flaky) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	down, lose := f.down, f.lose > 0
	if lose {
		f.lose--
	}
	f.mu.Unlock()
	if down {
		return nil, errors.New("connection refused")
	}
	resp, err := f.next.RoundTrip(r)
	if lose && err == nil {
		_ = resp.Body.Close()
		return nil, errors.New("connection reset after the request was sent")
	}
	return resp, err
}

func line(i int, level string) []byte {
	return []byte(fmt.Sprintf(`{"ts":"2026-11-07T12:00:%02d.000Z","level":%q,"msg":"line %d","service":"cloud.api","node":"A"}`, i%60, level, i))
}

// T02 (story 53.17): a network drop and its return leave W with every line once, in order:
// no gap and no duplicate, even when W took a batch whose answer was lost.
func TestForwardingSurvivesADropWithNoGapAndNoDuplicate(t *testing.T) {
	root := t.TempDir()
	wk := logkeeper.New(logkeeper.Config{Root: root, Node: "W", Free: func(string) int64 { return -1 }})
	rv := &Receiver{Keeper: wk, Token: "t0k", State: StatePath(root)}
	w := httptest.NewServer(rv)
	defer w.Close()
	net := &flaky{next: http.DefaultTransport}
	series := obs.New()
	f := &Forwarder{URL: w.URL, Token: "t0k", Node: "A", Service: "cloud.api",
		Client: &http.Client{Transport: net}, Metrics: series}
	ctx := context.Background()

	for i := 1; i <= 10; i++ {
		f.Add(line(i, "info"))
	}
	if err := f.Flush(ctx); err != nil || f.Queued() != 0 {
		t.Fatalf("first batch: %v, %d left", err, f.Queued())
	}
	// the network drops: lines wait, and the lag grows
	net.mu.Lock()
	net.down = true
	net.mu.Unlock()
	for i := 11; i <= 25; i++ {
		f.Add(line(i, "info"))
	}
	if err := f.Flush(ctx); err == nil || f.Queued() != 15 {
		t.Fatalf("while down: %v, %d queued", err, f.Queued())
	}
	// back, but the first answer is lost: W has the lines, the node sends them again
	net.mu.Lock()
	net.down, net.lose = false, 1
	net.mu.Unlock()
	if err := f.Flush(ctx); err == nil {
		t.Fatal("a lost answer is an error")
	}
	if f.Queued() != 15 {
		t.Fatalf("still queued after a lost answer: %d", f.Queued())
	}
	if err := f.Flush(ctx); err != nil || f.Queued() != 0 {
		t.Fatalf("resent: %v, %d left", err, f.Queued())
	}
	wk.Flush()
	got := wk.Read(logkeeper.Query{Limit: 1000})
	var msgs []string
	for i := len(got.Lines) - 1; i >= 0; i-- { // newest first: oldest first here
		var m struct {
			Msg  string `json:"msg"`
			Node string `json:"node"`
		}
		_ = json.Unmarshal(got.Lines[i], &m)
		if m.Node != "A" {
			t.Fatalf("a line lost its node: %s", got.Lines[i])
		}
		msgs = append(msgs, m.Msg)
	}
	var want []string
	for i := 1; i <= 25; i++ {
		want = append(want, fmt.Sprintf("line %d", i))
	}
	if strings.Join(msgs, "|") != strings.Join(want, "|") {
		t.Fatalf("W has:\n%v\nwant:\n%v", msgs, want)
	}
	// a restarted W reads how far it took each process's lines
	rv2 := &Receiver{Keeper: wk, Token: "t0k", State: StatePath(root)}
	rv2.load()
	if len(rv2.last) != 1 {
		t.Fatalf("W's state: %v", rv2.last)
	}
}

// A warn line is sent within a second; the rest wait for the batch.
func TestAWarnLineIsSentAtOnce(t *testing.T) {
	root := t.TempDir()
	wk := logkeeper.New(logkeeper.Config{Root: root, Node: "W", Free: func(string) int64 { return -1 }})
	var posts sync.WaitGroup
	posts.Add(1)
	var once sync.Once
	rv := &Receiver{Keeper: wk, Token: "t", State: StatePath(root)}
	w := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rv.ServeHTTP(rw, r)
		once.Do(posts.Done)
	}))
	defer w.Close()
	f := &Forwarder{URL: w.URL, Token: "t", Node: "A", Service: "cloud.relay", Metrics: obs.New()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.Run(ctx)
	start := time.Now()
	f.Add(line(1, "info"))
	f.Add(line(2, "warn"))
	done := make(chan struct{})
	go func() { posts.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the warn line was not sent within a second or so")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v", d)
	}
}

// W refuses a batch without its token, and a name that is not a name.
func TestTheReceiverChecksTheTokenAndTheNames(t *testing.T) {
	root := t.TempDir()
	rv := &Receiver{Keeper: logkeeper.New(logkeeper.Config{Root: root, Free: func(string) int64 { return -1 }}), Token: "t", State: StatePath(root)}
	post := func(tok string, b Batch) int {
		body, _ := json.Marshal(b)
		req := httptest.NewRequest("POST", IngestPath, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		rv.ServeHTTP(rec, req)
		return rec.Code
	}
	ok := Batch{Boot: "b1", Node: "A", Service: "cloud.api"}
	if c := post("wrong", ok); c != http.StatusUnauthorized {
		t.Fatalf("a wrong token: %d", c)
	}
	if c := post("t", Batch{Boot: "b1", Node: "../etc", Service: "cloud.api"}); c != http.StatusBadRequest {
		t.Fatalf("a node that is a path: %d", c)
	}
	if c := post("t", Batch{Boot: "b1", Node: "A..B", Service: "cloud.api"}); c != http.StatusBadRequest {
		t.Fatalf("a node holding ..: %d", c)
	}
	if c := post("t", ok); c != http.StatusOK {
		t.Fatalf("ok: %d", c)
	}
}

// Over the queue's bound the oldest info line goes first, warn lines are kept, and the drop
// is counted.
func TestAFullQueueDropsInfoFirstAndCountsIt(t *testing.T) {
	series := obs.New()
	f := &Forwarder{Max: 3, Service: "cloud.api", Metrics: series}
	f.Add(line(1, "warn"))
	f.Add(line(2, "info"))
	f.Add(line(3, "info"))
	f.Add(line(4, "info"))
	f.Add(line(5, "error"))
	var msgs []string
	for _, it := range f.queue {
		var m struct{ Msg string }
		_ = json.Unmarshal(it.Line, &m)
		msgs = append(msgs, m.Msg)
	}
	if strings.Join(msgs, "|") != "line 1|line 4|line 5" {
		t.Fatalf("kept %v", msgs)
	}
	var b bytes.Buffer
	_ = series.WriteMetrics(&b)
	if !strings.Contains(b.String(), `maktaba_log_dropped_total{service="cloud.api"} 2`) {
		t.Fatalf("not counted:\n%s", b.String())
	}
}

// The tee keeps each line on the node and queues it for W.
func TestTheTeeKeepsAndForwardsEachLine(t *testing.T) {
	root := t.TempDir()
	k := logkeeper.New(logkeeper.Config{Root: root, Node: "A", Free: func(string) int64 { return -1 }})
	f := &Forwarder{Service: "cloud.api", Metrics: obs.New()}
	tee := &Tee{Keeper: k, Forwarder: f, Origin: logkeeper.Origin{Service: "cloud.api"}}
	_, _ = io.WriteString(tee, string(line(1, "info"))+"\n"+string(line(2, "warn")))
	_, _ = io.WriteString(tee, "\n")
	k.Flush()
	if n := len(k.Read(logkeeper.Query{}).Lines); n != 2 || f.Queued() != 2 {
		t.Fatalf("kept %d, queued %d", n, f.Queued())
	}
}
