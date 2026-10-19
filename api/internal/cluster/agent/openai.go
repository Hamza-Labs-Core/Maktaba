package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// The OpenAI-compatible door (design-agent.md §6):
//
//	GET  /api/cluster/llm/v1/models
//	POST /api/cluster/llm/v1/chat/completions   op "chat"
//	POST /api/cluster/llm/v1/embeddings         op "embed"
//
// The request body goes to the runner unchanged as llm.request.body; the
// runner answers with llm.chunk frames whose data is an OpenAI object: a
// `chat.completion.chunk` per chunk when streaming, otherwise one chunk
// holding the whole `chat.completion` / embeddings response.

// maxLLMChunks caps what a non-streaming answer may buffer. The body cap
// is the channel's: the llm.request frame carrying it must seal within
// MaxFrame (RequestFits), else 413 too_large.
const maxLLMChunks = 100_000

type openAIError struct {
	Error struct {
		Message string  `json:"message"`
		Type    string  `json:"type"`
		Code    *string `json:"code"`
	} `json:"error"`
}

func writeOpenAIError(w http.ResponseWriter, status int, code, msg string) {
	var e openAIError
	e.Error.Message, e.Error.Type = msg, "invalid_request_error"
	if status >= 500 {
		e.Error.Type = "server_error"
	}
	if code != "" {
		e.Error.Code = &code
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(e)
}

func llmErrStatus(code string) (int, string) {
	switch code {
	case LLMNoModel:
		return http.StatusNotFound, "no machine in the cluster has this model"
	case LLMNoCapacity:
		return http.StatusServiceUnavailable, "every machine with this model stayed busy past the queue timeout"
	case LLMMachineGone:
		return http.StatusBadGateway, "the machine running the request disconnected"
	case LLMRuntimeError:
		return http.StatusBadGateway, "the model runtime failed"
	case LLMTimeout:
		return http.StatusGatewayTimeout, "the machine running the request stopped answering"
	case LLMBadRequest:
		return http.StatusBadRequest, "bad request"
	case LLMTooLarge:
		return http.StatusRequestEntityTooLarge, "the request or an answer is over the cluster channel's 4 MiB frame limit"
	}
	return http.StatusBadGateway, "the model failed: " + code
}

// requireUser: admins (admin token, admin session or admin JWT) and
// personal access tokens; an ordinary signed-in session gets 403.
func requireUser(w http.ResponseWriter, r *http.Request) bool {
	p := principal.FromContext(r.Context())
	switch {
	case p == nil:
		writeOpenAIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return false
	case !p.IsAdmin && p.Source != principal.SourcePAT:
		writeOpenAIError(w, http.StatusForbidden, "forbidden", "an admin or a personal access token is required")
		return false
	}
	return true
}

// LLMModels implements GET /api/cluster/llm/v1/models.
func (h *Hub) LLMModels(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	type model struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	data := []model{}
	for _, m := range h.router.Models() {
		data = append(data, model{ID: m.Model, Object: "model", OwnedBy: "maktaba-cluster"})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

// LLMChat implements POST /api/cluster/llm/v1/chat/completions.
func (h *Hub) LLMChat(w http.ResponseWriter, r *http.Request) { h.llmDoor(w, r, "chat") }

// LLMEmbeddings implements POST /api/cluster/llm/v1/embeddings.
func (h *Hub) LLMEmbeddings(w http.ResponseWriter, r *http.Request) { h.llmDoor(w, r, "embed") }

// httpSink buffers a request's output for the HTTP handler.
type httpSink struct {
	mu       sync.Mutex
	chunks   []json.RawMessage
	done     bool
	errCode  string
	overflow bool
	notify   chan struct{}
}

func newHTTPSink() *httpSink { return &httpSink{notify: make(chan struct{}, 1)} }

func (k *httpSink) poke() {
	select {
	case k.notify <- struct{}{}:
	default:
	}
}

func (k *httpSink) Chunk(data json.RawMessage) {
	k.mu.Lock()
	if len(k.chunks) >= maxLLMChunks {
		k.overflow = true
	} else {
		k.chunks = append(k.chunks, data)
	}
	k.mu.Unlock()
	k.poke()
}

func (k *httpSink) Done(_ json.RawMessage, e LLMError) {
	k.mu.Lock()
	k.done, k.errCode = true, e.Code
	k.mu.Unlock()
	k.poke()
}

func (k *httpSink) take() (chunks []json.RawMessage, done bool, errCode string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	chunks, k.chunks = k.chunks, nil
	if k.overflow {
		return chunks, true, "overflow"
	}
	return chunks, k.done, k.errCode
}

func (h *Hub) llmDoor(w http.ResponseWriter, r *http.Request, op string) {
	if !requireUser(w, r) {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxFrame+1))
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "the body could not be read")
		return
	}
	if len(body) > MaxFrame {
		status, msg := llmErrStatus(LLMTooLarge)
		writeOpenAIError(w, status, LLMTooLarge, msg)
		return
	}
	var head struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &head); err != nil || strings.TrimSpace(head.Model) == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "the body must be JSON with a model")
		return
	}
	stream := head.Stream && op == "chat"
	sink := newHTTPSink()
	req := &LLMRequest{Model: head.Model, Op: op, Body: body, Stream: stream, FromIP: remoteIP(r), Sink: sink}
	if !RequestFits(req) {
		status, msg := llmErrStatus(LLMTooLarge)
		writeOpenAIError(w, status, LLMTooLarge, msg)
		return
	}
	h.router.Submit(req)
	defer h.router.Cancel(req.ID())

	if stream {
		h.streamOpenAI(w, r, sink)
		return
	}
	var all []json.RawMessage
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sink.notify:
		}
		chunks, done, errCode := sink.take()
		all = append(all, chunks...)
		if !done {
			continue
		}
		if errCode != "" {
			status, msg := llmErrStatus(errCode)
			writeOpenAIError(w, status, errCode, msg)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(assemble(all))
		return
	}
}

// streamOpenAI writes chunks as `data: <json>` lines and `data: [DONE]`.
// An error before the first chunk is a plain HTTP error.
func (h *Hub) streamOpenAI(w http.ResponseWriter, r *http.Request, sink *httpSink) {
	rc := http.NewResponseController(w)
	started := false
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			if started {
				_, _ = w.Write([]byte(": keepalive\n\n"))
				_ = rc.Flush()
			}
			continue
		case <-sink.notify:
		}
		chunks, done, errCode := sink.take()
		if !started && (len(chunks) > 0 || (done && errCode == "")) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(http.StatusOK)
			started = true
		}
		if !started && done {
			status, msg := llmErrStatus(errCode)
			writeOpenAIError(w, status, errCode, msg)
			return
		}
		for _, c := range chunks {
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(c)
			_, _ = w.Write([]byte("\n\n"))
		}
		if done {
			if errCode != "" {
				_, msg := llmErrStatus(errCode)
				var e openAIError
				e.Error.Message, e.Error.Type, e.Error.Code = msg, "server_error", &errCode
				b, _ := json.Marshal(e)
				_, _ = w.Write([]byte("data: "))
				_, _ = w.Write(b)
				_, _ = w.Write([]byte("\n\n"))
			}
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		}
		_ = rc.Flush()
		if done {
			return
		}
	}
}

// assemble is the non-streaming answer: the runner's single response, or,
// when it streamed chat chunks anyway, one chat.completion built from them.
func assemble(chunks []json.RawMessage) []byte {
	if len(chunks) == 0 {
		return []byte(`{}`)
	}
	if len(chunks) == 1 {
		return chunks[0]
	}
	type delta struct {
		Choices []struct {
			Index        int     `json:"index"`
			FinishReason *string `json:"finish_reason"`
			Delta        struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Created int64           `json:"created"`
		Object  string          `json:"object"`
		Usage   json.RawMessage `json:"usage"`
	}
	var first delta
	if json.Unmarshal(chunks[0], &first) != nil || first.Object != "chat.completion.chunk" {
		return chunks[len(chunks)-1]
	}
	var content strings.Builder
	var finish *string
	var usage json.RawMessage
	role := "assistant"
	for _, c := range chunks {
		var d delta
		if json.Unmarshal(c, &d) != nil {
			continue
		}
		if len(d.Usage) > 0 && string(d.Usage) != "null" {
			usage = d.Usage
		}
		for _, ch := range d.Choices {
			if ch.Index != 0 {
				continue
			}
			if ch.Delta.Role != "" {
				role = ch.Delta.Role
			}
			content.WriteString(ch.Delta.Content)
			if ch.FinishReason != nil {
				finish = ch.FinishReason
			}
		}
	}
	out := map[string]any{
		"id": first.ID, "object": "chat.completion", "created": first.Created, "model": first.Model,
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": role, "content": content.String()},
			"finish_reason": finish}},
	}
	if usage != nil {
		out["usage"] = usage
	}
	b, err := json.Marshal(out)
	if err != nil {
		return chunks[len(chunks)-1]
	}
	return b
}
