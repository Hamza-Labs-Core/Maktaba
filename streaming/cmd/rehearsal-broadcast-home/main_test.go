package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/broadcast"
)

// The test pattern is software only, at real time, with the clock and the frame number burned in.
func TestPatternArgs(t *testing.T) {
	a := PatternArgs(output, patternFont)
	joined := strings.Join(a, " ")
	for _, want := range []string{"-re", "testsrc2=size=854x480:rate=25", "%{localtime", "%{n}", "libx264", "-g 50", "-f mpegts pipe:1", "fontfile=" + patternFont} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q missing from %s", want, joined)
		}
	}
	if strings.Contains(joined, "videotoolbox") {
		t.Errorf("the rehearsal never uses VideoToolbox: %s", joined)
	}
	if strings.Contains(strings.Join(PatternArgs(output, ""), " "), "fontfile") {
		t.Error("no font: ffmpeg's own")
	}
}

// What the broadcaster writes back (a refusal, the kill switch) is what the next sync reads.
func TestMemStore(t *testing.T) {
	s := &memStore{}
	ch := uuid.New()
	s.publish(broadcast.Job{Channel: ch, State: "live"})
	if err := s.SetState(context.Background(), ch, "revoked", "notice"); err != nil {
		t.Fatal(err)
	}
	jobs, _ := s.Jobs(context.Background())
	if len(jobs) != 1 || jobs[0].State != "revoked" {
		t.Fatalf("jobs: %+v", jobs)
	}
	_ = s.Report(context.Background(), ch, map[string]any{"segments": map[string]int{"src": 3}})
	state, detail, rep := s.snapshot()
	if state != "revoked" || detail != "notice" || rep == nil {
		t.Fatalf("snapshot: %s %s %v", state, detail, rep)
	}
	s.unpublish()
	if jobs, _ := s.Jobs(context.Background()); jobs[0].State != "draft" {
		t.Fatalf("unpublished: %+v", jobs)
	}
}

// The proxy counter logs each segment PUT with its answer, and nothing else.
func TestCountingTransport(t *testing.T) {
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			time.Sleep(30 * time.Millisecond) // a busy edge: the log keeps how long it took
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer edge.Close()
	log := &putLog{}
	c := &http.Client{Transport: &countingTransport{next: http.DefaultTransport, log: log}}
	for _, p := range []struct{ method, path string }{
		{http.MethodPut, "/v1/ingest/abc/src/42.ts"},
		{http.MethodPut, "/v1/ingest/abc/slate/0.ts"},
		{http.MethodPost, "/v1/ingest/abc/state"},
	} {
		req, _ := http.NewRequest(p.method, edge.URL+p.path, strings.NewReader("x"))
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	got := log.snapshot()
	if len(got) != 2 || got[0].Rung != "src" || got[0].Seq != 42 || got[0].Status != 201 || got[1].Rung != "slate" {
		t.Fatalf("puts: %+v", got)
	}
	if time.Since(time.UnixMilli(got[0].AtMS)) > time.Minute {
		t.Fatalf("time: %d", got[0].AtMS)
	}
	if got[0].TookMS < 30 || got[0].Err != "" {
		t.Fatalf("took %d ms, err %q", got[0].TookMS, got[0].Err)
	}
}

// A PUT with no answer (the edge gone) is logged with status 0 and the client's error, so the
// fan-out can tell an unanswered PUT from a refusal.
func TestCountingTransportNoAnswer(t *testing.T) {
	edge := httptest.NewServer(http.NotFoundHandler())
	url := edge.URL
	edge.Close()
	log := &putLog{}
	c := &http.Client{Transport: &countingTransport{next: http.DefaultTransport, log: log}}
	req, _ := http.NewRequest(http.MethodPut, url+"/v1/ingest/abc/480p/7.ts", strings.NewReader("x"))
	if resp, err := c.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatal("a closed edge answered")
	}
	got := log.snapshot()
	if len(got) != 1 || got[0].Rung != "480p" || got[0].Seq != 7 || got[0].Status != 0 || got[0].Err == "" {
		t.Fatalf("puts: %+v", got)
	}
}

// The control port publishes, reports and refuses a bad body.
func TestControlPort(t *testing.T) {
	h := newHome("ffmpeg-not-run", t.TempDir(), nil)
	srv := httptest.NewServer(h.routes())
	defer srv.Close()
	resp, _ := http.Post(srv.URL+"/publish", "application/json", strings.NewReader(`{}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty publish: %d", resp.StatusCode)
	}
	resp, _ = http.Post(srv.URL+"/publish", "application/json",
		strings.NewReader(`{"channel":"abc","token":"mkbi1.x.y","ingest_url":"http://edge:8787","rungs":["src","480p","360p"],"name":"Rehearsal"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish: %d", resp.StatusCode)
	}
	jobs, _ := h.store.Jobs(context.Background())
	if len(jobs) != 1 || jobs[0].Creds.Channel != "abc" || !slices.Equal(jobs[0].Rungs, []string{"src", "480p", "360p"}) || jobs[0].State != "live" {
		t.Fatalf("job: %+v", jobs)
	}
	_, _ = http.Post(srv.URL+"/encoder/stop", "", nil)
	resp, _ = http.Get(srv.URL + "/metrics")
	var m struct {
		State     string `json:"state"`
		EncoderOn bool   `json:"encoder_on"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&m)
	if m.State != "live" || m.EncoderOn {
		t.Fatalf("metrics: %+v", m)
	}
}

// A slowed uplink: every 2nd segment PUT's body takes its time to arrive (the edge's clock starts
// at the request, so its upload-to-visible grows); the rest go at once, and {} restores the link.
func TestUplinkSlowsEveryNthPut(t *testing.T) {
	var arrived []time.Duration
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		_, _ = io.Copy(io.Discard, r.Body)
		arrived = append(arrived, time.Since(t0))
		w.WriteHeader(http.StatusCreated)
	}))
	defer edge.Close()
	h := newHome("ffmpeg-not-run", t.TempDir(), nil)
	ctl := httptest.NewServer(h.routes())
	defer ctl.Close()
	for _, bad := range []string{`{"every":-1}`, `{"every":2,"body_ms":30000}`, `nope`} {
		resp, _ := http.Post(ctl.URL+"/uplink", "application/json", strings.NewReader(bad))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("uplink %s: %d", bad, resp.StatusCode)
		}
	}
	resp, _ := http.Post(ctl.URL+"/uplink", "application/json", strings.NewReader(`{"every":2,"body_ms":400}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("uplink: %d", resp.StatusCode)
	}
	put := func(seq int) {
		body := strings.Repeat("x", 64<<10)
		req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/v1/ingest/abc/480p/%d.ts", edge.URL, seq), strings.NewReader(body))
		res, err := h.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
	}
	for i := 1; i <= 4; i++ {
		put(i)
	}
	_, _ = http.Post(ctl.URL+"/uplink", "application/json", strings.NewReader(`{}`))
	put(5)
	if len(arrived) != 5 {
		t.Fatalf("PUTs: %v", arrived)
	}
	for i, d := range arrived {
		slow := i == 1 || i == 3
		if slow && d < 300*time.Millisecond || !slow && d > 200*time.Millisecond {
			t.Errorf("PUT %d's body took %s (slow: %v)", i+1, d, slow)
		}
	}
	if puts := h.puts.snapshot(); len(puts) != 5 || puts[1].TookMS < 300 {
		t.Errorf("the PUT log: %+v", puts)
	}
}
