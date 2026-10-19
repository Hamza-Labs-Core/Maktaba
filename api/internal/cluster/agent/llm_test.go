package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRunners records what the router sends to each session.
type fakeRunners struct {
	mu     sync.Mutex
	frames map[string][]map[string]any
	gone   map[string]bool
}

func (f *fakeRunners) send(session string, frame any) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gone[session] {
		return false
	}
	b, _ := json.Marshal(frame)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	f.frames[session] = append(f.frames[session], m)
	return true
}

func (f *fakeRunners) requests(session string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.frames[session]...)
}

// recSink records a request's output.
type recSink struct {
	mu     sync.Mutex
	chunks []string
	done   chan string
	usage  json.RawMessage
}

func newRecSink() *recSink { return &recSink{done: make(chan string, 1)} }

func (s *recSink) Chunk(d json.RawMessage) {
	s.mu.Lock()
	s.chunks = append(s.chunks, string(d))
	s.mu.Unlock()
}

func (s *recSink) Done(u json.RawMessage, e LLMError) {
	s.mu.Lock()
	s.usage = u
	s.mu.Unlock()
	s.done <- e.Code
}

func (s *recSink) wait(t *testing.T) string {
	t.Helper()
	select {
	case e := <-s.done:
		return e
	case <-time.After(3 * time.Second):
		t.Fatal("request never finished")
		return ""
	}
}

func (s *recSink) pending() bool { return len(s.done) == 0 }

func newTestRouter(timeout time.Duration, now func() time.Time) (*Router, *fakeRunners, *[]LLMEvent) {
	fr := &fakeRunners{frames: map[string][]map[string]any{}, gone: map[string]bool{}}
	var mu sync.Mutex
	events := &[]LLMEvent{}
	rt := NewRouter(RouterConfig{QueueTimeout: timeout, Now: now, Send: fr.send,
		Notify: func(e LLMEvent) { mu.Lock(); *events = append(*events, e); mu.Unlock() }})
	return rt, fr, events
}

func runtimes(t *testing.T, rs ...map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(rs)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ollama(maxPar int, models ...map[string]any) map[string]any {
	return map[string]any{"provider": "ollama", "base_url": "http://127.0.0.1:11434", "max_parallel": maxPar, "models": models}
}

func model(name string, loaded bool) map[string]any {
	return map[string]any{"name": name, "loaded": loaded}
}

func lastRunner(fr *fakeRunners, sessions ...string) string {
	for _, s := range sessions {
		if len(fr.requests(s)) > 0 {
			return s
		}
	}
	return ""
}

func TestRouterOrder(t *testing.T) {
	t0 := time.Now()
	t.Run("loaded first", func(t *testing.T) {
		rt, fr, _ := newTestRouter(time.Second, time.Now)
		rt.UpdateHost("a", "10.0.0.1", t0, runtimes(t, ollama(1, model("llama3", false))))
		rt.UpdateHost("b", "10.0.0.2", t0.Add(time.Second), runtimes(t, ollama(1, model("llama3", true))))
		rt.Submit(&LLMRequest{Model: "llama3", Op: "chat", FromIP: "10.0.0.1", Sink: newRecSink()})
		if got := lastRunner(fr, "a", "b"); got != "b" {
			t.Fatalf("ran on %q", got)
		}
	})
	t.Run("least loaded relative to max_parallel", func(t *testing.T) {
		rt, fr, _ := newTestRouter(time.Second, time.Now)
		rt.UpdateHost("a", "10.0.0.1", t0, runtimes(t, ollama(4, model("m", true))))
		rt.UpdateHost("b", "10.0.0.2", t0, runtimes(t, ollama(1, model("m", true))))
		// a: 0/4, b: 0/1 → tie on load, earlier session order (a) first
		rt.Submit(&LLMRequest{Model: "m", Op: "chat", Sink: newRecSink()})
		// a: 1/4 = .25, b: 0/1 = 0 → b
		rt.Submit(&LLMRequest{Model: "m", Op: "chat", Sink: newRecSink()})
		// a: .25, b: 1/1 full → a
		rt.Submit(&LLMRequest{Model: "m", Op: "chat", Sink: newRecSink()})
		if len(fr.requests("a")) != 2 || len(fr.requests("b")) != 1 {
			t.Fatalf("a=%d b=%d", len(fr.requests("a")), len(fr.requests("b")))
		}
	})
	t.Run("requester's machine breaks ties", func(t *testing.T) {
		rt, fr, _ := newTestRouter(time.Second, time.Now)
		rt.UpdateHost("a", "10.0.0.1", t0, runtimes(t, ollama(1, model("m", true))))
		rt.UpdateHost("b", "10.0.0.2", t0.Add(time.Second), runtimes(t, ollama(1, model("m", true))))
		rt.Submit(&LLMRequest{Model: "m", Op: "embed", FromIP: "10.0.0.2", Sink: newRecSink()})
		if got := lastRunner(fr, "a", "b"); got != "b" {
			t.Fatalf("ran on %q", got)
		}
	})
	t.Run("ollama latest tag", func(t *testing.T) {
		rt, fr, _ := newTestRouter(time.Second, time.Now)
		rt.UpdateHost("a", "10.0.0.1", t0, runtimes(t, ollama(1, model("qwen2.5:latest", true))))
		rt.Submit(&LLMRequest{Model: "QWEN2.5", Op: "chat", Sink: newRecSink()})
		req := fr.requests("a")
		if len(req) != 1 || req[0]["t"] != "llm.request" || req[0]["model"] != "QWEN2.5" || req[0]["op"] != "chat" {
			t.Fatalf("frames = %v", req)
		}
	})
}

func TestRouterQueueFIFOAndDone(t *testing.T) {
	rt, fr, events := newTestRouter(5*time.Second, time.Now)
	rt.UpdateHost("a", "10.0.0.1", time.Now(), runtimes(t, ollama(1, model("m", true))))
	s1, s2, s3 := newRecSink(), newRecSink(), newRecSink()
	r1 := &LLMRequest{Model: "m", Op: "chat", Stream: true, FromSession: "req", Sink: s1}
	r2 := &LLMRequest{Model: "m", Op: "chat", Sink: s2}
	r3 := &LLMRequest{Model: "m", Op: "chat", Sink: s3}
	rt.Submit(r1)
	rt.Submit(r2)
	rt.Submit(r3)
	if n := len(fr.requests("a")); n != 1 {
		t.Fatalf("started %d", n)
	}
	// output flows to the requester; a stranger's chunk is ignored
	rt.Chunk("a", r1.ID(), json.RawMessage(`{"x":1}`))
	rt.Chunk("intruder", r1.ID(), json.RawMessage(`{"x":2}`))
	rt.Done("a", r1.ID(), json.RawMessage(`{"prompt_tokens":3,"completion_tokens":5,"ms":40}`), LLMError{})
	if e := s1.wait(t); e != "" || len(s1.chunks) != 1 || !strings.Contains(string(s1.usage), "completion_tokens") {
		t.Fatalf("r1: %q %v", e, s1.chunks)
	}
	// the slot frees: r2 (not r3) starts
	reqs := fr.requests("a")
	if len(reqs) != 2 || reqs[1]["id"] != r2.ID() || !s3.pending() {
		t.Fatalf("after done: %v", reqs)
	}
	rt.Done("a", r2.ID(), nil, LLMError{Code: "runtime_error", Detail: "oom"})
	if e := s2.wait(t); e != "runtime_error" {
		t.Fatalf("r2 = %q", e)
	}
	if reqs := fr.requests("a"); len(reqs) != 3 || reqs[2]["id"] != r3.ID() {
		t.Fatal("r3 did not start")
	}
	states := map[string]bool{}
	for _, e := range *events {
		states[e.State] = true
		if e.ID == r1.ID() && e.State == "done" && (e.FromSession == nil || *e.FromSession != "req" || *e.ToSession != "a") {
			t.Fatalf("event = %+v", e)
		}
	}
	for _, s := range []string{"queued", "started", "done", "failed"} {
		if !states[s] {
			t.Fatalf("no %s event in %+v", s, *events)
		}
	}
}

func TestRouterFailures(t *testing.T) {
	t.Run("no model", func(t *testing.T) {
		rt, _, _ := newTestRouter(time.Second, time.Now)
		s := newRecSink()
		rt.Submit(&LLMRequest{Model: "ghost", Op: "chat", Sink: s})
		if e := s.wait(t); e != LLMNoModel {
			t.Fatalf("= %q", e)
		}
	})
	t.Run("bad op", func(t *testing.T) {
		rt, _, _ := newTestRouter(time.Second, time.Now)
		s := newRecSink()
		rt.Submit(&LLMRequest{Model: "m", Op: "sing", Sink: s})
		if e := s.wait(t); e != LLMBadRequest {
			t.Fatalf("= %q", e)
		}
	})
	t.Run("queue timeout", func(t *testing.T) {
		rt, _, _ := newTestRouter(80*time.Millisecond, time.Now)
		rt.UpdateHost("a", "", time.Now(), runtimes(t, ollama(1, model("m", true))))
		rt.Submit(&LLMRequest{Model: "m", Op: "chat", Sink: newRecSink()})
		s := newRecSink()
		start := time.Now()
		rt.Submit(&LLMRequest{Model: "m", Op: "chat", Sink: s})
		if e := s.wait(t); e != LLMNoCapacity || time.Since(start) < 60*time.Millisecond {
			t.Fatalf("= %q after %v", e, time.Since(start))
		}
	})
	t.Run("machine gone", func(t *testing.T) {
		rt, _, _ := newTestRouter(time.Second, time.Now)
		rt.UpdateHost("a", "", time.Now(), runtimes(t, ollama(1, model("m", true))))
		running, queuedOwn := newRecSink(), newRecSink()
		rt.Submit(&LLMRequest{Model: "m", Op: "chat", Sink: running})
		rt.Submit(&LLMRequest{Model: "m", Op: "chat", FromSession: "a", Sink: queuedOwn})
		rt.HostGone("a")
		if e := running.wait(t); e != LLMMachineGone {
			t.Fatalf("= %q", e)
		}
		if snap := rt.Snapshot(); len(snap["requests"].([]LLMEvent)) != 0 || len(snap["models"].([]ModelInfo)) != 0 {
			t.Fatalf("snapshot = %+v", snap)
		}
	})
	t.Run("runner unreachable at dispatch", func(t *testing.T) {
		rt, fr, _ := newTestRouter(time.Second, time.Now)
		fr.gone["a"] = true
		rt.UpdateHost("a", "", time.Now(), runtimes(t, ollama(1, model("m", true))))
		s := newRecSink()
		rt.Submit(&LLMRequest{Model: "m", Op: "chat", Sink: s})
		if e := s.wait(t); e != LLMMachineGone {
			t.Fatalf("= %q", e)
		}
	})
	t.Run("idle timeout", func(t *testing.T) {
		clk := &clock{t: time.Now()}
		rt, _, _ := newTestRouter(time.Second, clk.Now)
		rt.UpdateHost("a", "", clk.Now(), runtimes(t, ollama(1, model("m", true))))
		s := newRecSink()
		rt.Submit(&LLMRequest{Model: "m", Op: "chat", Sink: s})
		rt.Sweep()
		if !s.pending() {
			t.Fatal("swept too early")
		}
		clk.Set(clk.Now().Add(DefaultIdleTimeout + time.Second))
		rt.Sweep()
		if e := s.wait(t); e != LLMTimeout {
			t.Fatalf("= %q", e)
		}
	})
	t.Run("models update keeps in-flight counts", func(t *testing.T) {
		rt, fr, _ := newTestRouter(time.Second, time.Now)
		rt.UpdateHost("a", "", time.Now(), runtimes(t, ollama(1, model("m", true))))
		rt.Submit(&LLMRequest{Model: "m", Op: "chat", Sink: newRecSink()})
		rt.UpdateHost("a", "", time.Now(), runtimes(t, ollama(1, model("m", true), model("n", false))))
		s := newRecSink()
		rt.Submit(&LLMRequest{Model: "n", Op: "chat", Sink: s})
		if len(fr.requests("a")) != 1 || !s.pending() {
			t.Fatal("the busy runtime took a second request")
		}
	})
}

// fakeRunner answers llm.request frames on a test agent.
func fakeRunner(t *testing.T, a *testAgent, chunks []string, usage map[string]any, errCode string) map[string]any {
	t.Helper()
	req := a.recvType("llm.request")
	for _, c := range chunks {
		a.send(map[string]any{"t": "llm.chunk", "id": req["id"], "data": json.RawMessage(c)})
	}
	done := map[string]any{"t": "llm.done", "id": req["id"], "usage": usage}
	if errCode != "" {
		done["error"] = errCode
	}
	a.send(done)
	return req
}

func announce(a *testAgent, models ...map[string]any) {
	a.send(map[string]any{"t": "llm.models", "runtimes": []any{ollama(1, models...)}})
}

func TestOpenAIDoorStreaming(t *testing.T) {
	f := newFixture(t)
	runner, _ := f.node("gpu-box")
	b := f.browser("admin")
	b.subscribe(TopicLLM)
	announce(runner, model("llama3", true))
	waitFor(t, "model known", func() bool { return len(f.hub.Router().Models()) == 1 })

	resp := f.do("GET", "/api/cluster/llm/v1/models", "", "pat")
	var models struct {
		Object string `json:"object"`
		Data   []struct {
			ID     string `json:"id"`
			Object string `json:"object"`
		} `json:"data"`
	}
	decode(t, resp, &models)
	if models.Object != "list" || len(models.Data) != 1 || models.Data[0].ID != "llama3" || models.Data[0].Object != "model" {
		t.Fatalf("models = %+v", models)
	}

	chunks := []string{
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"llama3","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`,
		`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"llama3","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}`,
	}
	got := make(chan map[string]any, 1)
	go func() {
		got <- fakeRunner(t, runner, chunks, map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "ms": 9}, "")
	}()
	resp = f.do("POST", "/api/cluster/llm/v1/chat/completions",
		`{"model":"llama3","stream":true,"messages":[{"role":"user","content":"hi"}]}`, "pat")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var lines []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if l := sc.Text(); strings.HasPrefix(l, "data: ") {
			lines = append(lines, strings.TrimPrefix(l, "data: "))
		}
	}
	if len(lines) != 3 || lines[0] != chunks[0] || lines[1] != chunks[1] || lines[2] != "[DONE]" {
		t.Fatalf("stream = %q", lines)
	}
	req := <-got
	body, _ := json.Marshal(req["body"])
	if req["op"] != "chat" || req["stream"] != true || !strings.Contains(string(body), `"content":"hi"`) {
		t.Fatalf("llm.request = %v", req)
	}
	// browsers saw it start and finish, with usage and no text
	var states []string
	for len(states) < 2 {
		m := b.nextType("llm")
		var ev LLMEvent
		_ = json.Unmarshal(m.Data, &ev)
		states = append(states, ev.State)
		if ev.State == "done" && (!strings.Contains(string(ev.Usage), "completion_tokens") || ev.FromSession != nil) {
			t.Fatalf("done event = %s", m.Data)
		}
		if strings.Contains(string(m.Data), "Hel") {
			t.Fatal("output text leaked into the llm event")
		}
	}
	if states[0] != "started" || states[1] != "done" {
		t.Fatalf("states = %v", states)
	}
}

func TestOpenAIDoorNonStreamingAndErrors(t *testing.T) {
	f := newFixture(t)
	runner, _ := f.node("cpu-box")
	announce(runner, model("nomic-embed", true), model("llama3", true))
	waitFor(t, "models", func() bool { return len(f.hub.Router().Models()) == 2 })

	// embeddings: one chunk is the answer
	go fakeRunner(t, runner, []string{`{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}]}`}, nil, "")
	resp := f.do("POST", "/api/cluster/llm/v1/embeddings", `{"model":"nomic-embed","input":"x"}`, "admin")
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"embedding":[0.1,0.2]`) {
		t.Fatalf("embeddings: %d %s", resp.StatusCode, raw)
	}

	// a runner that streamed anyway: assembled into one chat.completion
	go fakeRunner(t, runner, []string{
		`{"id":"c2","object":"chat.completion.chunk","model":"llama3","choices":[{"index":0,"delta":{"role":"assistant","content":"a"}}]}`,
		`{"id":"c2","object":"chat.completion.chunk","model":"llama3","choices":[{"index":0,"delta":{"content":"b"},"finish_reason":"stop"}]}`,
	}, nil, "")
	resp = f.do("POST", "/api/cluster/llm/v1/chat/completions", `{"model":"llama3","messages":[]}`, "admin")
	var cc struct {
		Object  string `json:"object"`
		Choices []struct {
			Message      struct{ Content string } `json:"message"`
			FinishReason string                   `json:"finish_reason"`
		} `json:"choices"`
	}
	decode(t, resp, &cc)
	if cc.Object != "chat.completion" || len(cc.Choices) != 1 || cc.Choices[0].Message.Content != "ab" || cc.Choices[0].FinishReason != "stop" {
		t.Fatalf("assembled = %+v", cc)
	}

	type oerr struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	for _, c := range []struct {
		path, body string
		status     int
		code       string
		runnerErr  string
	}{
		{"/api/cluster/llm/v1/chat/completions", `{"model":"ghost","stream":true}`, 404, LLMNoModel, ""},
		{"/api/cluster/llm/v1/chat/completions", `{"stream":true}`, 400, "invalid_request", ""},
		{"/api/cluster/llm/v1/chat/completions", `not json`, 400, "invalid_request", ""},
		{"/api/cluster/llm/v1/chat/completions", `{"model":"llama3","stream":true}`, 502, "runtime_down", "runtime_down"},
	} {
		if c.runnerErr != "" {
			go fakeRunner(t, runner, nil, nil, c.runnerErr)
		}
		resp := f.do("POST", c.path, c.body, "admin")
		var e oerr
		decode(t, resp, &e)
		if resp.StatusCode != c.status || e.Error.Code != c.code {
			t.Fatalf("%s %s: %d %+v", c.path, c.body, resp.StatusCode, e)
		}
	}
	// no principal; an ordinary signed-in user
	if r := f.do("GET", "/api/cluster/llm/v1/models", "", ""); r.StatusCode != 401 {
		t.Fatalf("anonymous: %d", r.StatusCode)
	}
	for _, p := range []string{"/api/cluster/llm/v1/models", "/api/cluster/llm/v1/chat/completions", "/api/cluster/llm/v1/embeddings"} {
		method := "POST"
		if strings.HasSuffix(p, "models") {
			method = "GET"
		}
		if r := f.do(method, p, `{"model":"llama3"}`, "viewer"); r.StatusCode != 403 {
			t.Fatalf("viewer %s: %d", p, r.StatusCode)
		}
	}
}

func TestOpenAIStreamMachineGoneMidway(t *testing.T) {
	f := newFixture(t)
	runner, _ := f.node("flaky")
	announce(runner, model("m", true))
	waitFor(t, "model", func() bool { return len(f.hub.Router().Models()) == 1 })
	go func() {
		req := runner.recvType("llm.request")
		runner.send(map[string]any{"t": "llm.chunk", "id": req["id"], "data": json.RawMessage(`{"object":"chat.completion.chunk","choices":[]}`)})
		time.Sleep(50 * time.Millisecond)
		_ = runner.c.CloseNow()
	}()
	resp := f.do("POST", "/api/cluster/llm/v1/chat/completions", `{"model":"m","stream":true}`, "admin")
	raw, _ := io.ReadAll(resp.Body)
	s := string(raw)
	if resp.StatusCode != 200 || !strings.Contains(s, `"code":"machine_gone"`) || !strings.HasSuffix(strings.TrimSpace(s), "data: [DONE]") {
		t.Fatalf("stream = %d %s", resp.StatusCode, s)
	}
}

func TestProcessToProcessLLM(t *testing.T) {
	f := newFixture(t)
	runner, _ := f.node("runner")
	asker, _ := f.node("asker")
	announce(runner, model("m", true))
	waitFor(t, "model", func() bool { return len(f.hub.Router().Models()) == 1 })

	asker.send(map[string]any{"t": "llm.request", "id": "mine-1", "model": "m", "op": "generate",
		"body": map[string]any{"prompt": "x"}, "stream": true})
	req := runner.recvType("llm.request")
	if req["id"] == "mine-1" || req["op"] != "generate" {
		t.Fatalf("runner got %v", req)
	}
	runner.send(map[string]any{"t": "llm.chunk", "id": req["id"], "data": map[string]any{"response": "y"}})
	runner.send(map[string]any{"t": "llm.done", "id": req["id"], "usage": map[string]any{"prompt_tokens": 1}})
	ch := asker.recvType("llm.chunk")
	if ch["id"] != "mine-1" || ch["data"].(map[string]any)["response"] != "y" {
		t.Fatalf("chunk = %v", ch)
	}
	done := asker.recvType("llm.done")
	if done["id"] != "mine-1" || done["error"] != nil {
		t.Fatalf("done = %v", done)
	}
	// nobody has it
	asker.send(map[string]any{"t": "llm.request", "id": "mine-2", "model": "ghost", "op": "chat", "body": map[string]any{}})
	done = asker.recvType("llm.done")
	if e, _ := done["error"].(map[string]any); done["id"] != "mine-2" || e["code"] != LLMNoModel {
		t.Fatalf("no model = %v", done)
	}
}

func TestLLMCancel(t *testing.T) {
	f := newFixture(t)
	runner, _ := f.node("runner")
	asker, _ := f.node("asker")
	announce(runner, model("m", true))
	waitFor(t, "model", func() bool { return len(f.hub.Router().Models()) == 1 })

	// the requester cancels: the runner is told, still ends with llm.done
	asker.send(map[string]any{"t": "llm.request", "id": "q1", "model": "m", "op": "chat", "body": map[string]any{}, "stream": true})
	req := runner.recvType("llm.request")
	asker.send(map[string]any{"t": "llm.cancel", "id": "q1"})
	if c := runner.recvType("llm.cancel"); c["id"] != req["id"] {
		t.Fatalf("cancel = %v", c)
	}
	runner.send(map[string]any{"t": "llm.chunk", "id": req["id"], "data": map[string]any{"late": true}})
	runner.send(map[string]any{"t": "llm.done", "id": req["id"], "error": map[string]any{"code": "cancelled"}})
	done := asker.recvType("llm.done")
	if e, _ := done["error"].(map[string]any); e["code"] != LLMCancelled {
		t.Fatalf("done = %v", done)
	}

	// the requester leaves: the runner is told to stop
	asker.send(map[string]any{"t": "llm.request", "id": "q2", "model": "m", "op": "chat", "body": map[string]any{}})
	req = runner.recvType("llm.request")
	_ = asker.c.CloseNow()
	if c := runner.recvType("llm.cancel"); c["id"] != req["id"] {
		t.Fatalf("cancel on leave = %v", c)
	}
	runner.send(map[string]any{"t": "llm.done", "id": req["id"], "error": map[string]any{"code": "cancelled"}})

	// an HTTP client that goes away: same
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		r, _ := http.NewRequestWithContext(ctx, "POST", f.srv.URL+"/api/cluster/llm/v1/chat/completions",
			strings.NewReader(`{"model":"m","stream":true}`))
		r.Header.Set("X-Test-User", "admin")
		if resp, err := http.DefaultClient.Do(r); err == nil {
			_ = resp.Body.Close()
		}
	}()
	req = runner.recvType("llm.request")
	cancel()
	if c := runner.recvType("llm.cancel"); c["id"] != req["id"] {
		t.Fatalf("cancel on HTTP leave = %v", c)
	}
}
