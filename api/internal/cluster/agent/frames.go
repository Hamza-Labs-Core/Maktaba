package agent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// dispatch handles one opened frame (design-agent.md §5). Unknown types
// are ignored (a newer agent may send more; the counter still moved).
func (h *Hub) dispatch(s *Session, t string, raw []byte) {
	if s.certOnly && t != "ping" && t != "pong" && t != "cert.csr" {
		return
	}
	switch t {
	case "cert.csr":
		h.onCertCSR(s, raw)
	case "ping":
		var f struct {
			TS json.RawMessage `json:"ts"`
		}
		_ = json.Unmarshal(raw, &f)
		pong := map[string]any{"t": "pong"}
		if len(f.TS) > 0 {
			pong["ts"] = f.TS
		}
		s.send(pong)
	case "pong":
	case "metrics":
		m := stripT(raw)
		s.mu.Lock()
		s.metrics = m
		s.mu.Unlock()
		h.metricsToCache(s, m)
		h.onMetrics(s, m)
	case "logs":
		var f struct {
			Records []json.RawMessage `json:"records"`
		}
		if json.Unmarshal(raw, &f) == nil {
			if len(f.Records) > maxLogRecords {
				f.Records = f.Records[:maxLogRecords]
			}
			h.logsToCache(s, f.Records)
			h.onLogs(s, f.Records)
		}
	case "job":
		h.onJob(s, stripT(raw))
	case "dropped":
		h.event(map[string]any{"event": "dropped", "session": s.ID, "ip": s.IP, "worker_id": s.WorkerID,
			"detail": stripT(raw)})
	case "llm.models":
		var f struct {
			Runtimes json.RawMessage `json:"runtimes"`
		}
		if json.Unmarshal(raw, &f) != nil {
			return
		}
		if len(f.Runtimes) == 0 || string(f.Runtimes) == "null" {
			f.Runtimes = json.RawMessage("[]")
		}
		s.mu.Lock()
		s.runtimes = f.Runtimes
		s.mu.Unlock()
		h.router.UpdateHost(s.ID, s.IP, s.ConnectedAt, f.Runtimes)
		h.publish(TopicProcesses, "process", processEvent{Event: "updated", Session: &s.ID, Process: ptr(s.view())})
	case "llm.request":
		var f LLMRequestFrame
		if json.Unmarshal(raw, &f) != nil || f.ID == "" {
			return
		}
		h.router.Submit(&LLMRequest{
			ClientID: f.ID, Model: f.Model, Op: f.Op, Body: f.Body, Stream: f.Stream,
			FromSession: s.ID, FromIP: s.IP, Sink: &sessionSink{s: s, id: f.ID},
		})
	case "llm.cancel":
		var f struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &f) == nil {
			h.router.CancelFrom(s.ID, f.ID)
		}
	case "llm.chunk":
		var f struct {
			ID   string          `json:"id"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &f) == nil {
			h.router.Chunk(s.ID, f.ID, f.Data)
		}
	case "llm.done":
		var f struct {
			ID    string          `json:"id"`
			Usage json.RawMessage `json:"usage"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(raw, &f) == nil {
			h.router.Done(s.ID, f.ID, f.Usage, parseLLMError(f.Error))
		}
	case "svc.request":
		h.onRelayRequest(s, raw)
	case "svc.cancel":
		var f struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &f) == nil {
			s.cancelRelay(f.ID)
		}
	case "update.state":
		h.onUpdateState(s, raw)
	case "access.request":
		h.onAccessRequest(s, raw)
	case "cmd.result":
		var f CmdResult
		if json.Unmarshal(raw, &f) == nil {
			s.deliverCmd(f)
		}
	}
}

// parseLLMError reads llm.done's error: {code, detail} (or a bare code).
func parseLLMError(raw json.RawMessage) LLMError {
	if len(raw) == 0 || string(raw) == "null" {
		return LLMError{}
	}
	var e LLMError
	if json.Unmarshal(raw, &e) == nil && e.Code != "" {
		return e
	}
	var code string
	if json.Unmarshal(raw, &code) == nil && code != "" {
		return LLMError{Code: code}
	}
	return LLMError{Code: LLMRuntimeError}
}

// stripT drops the "t" key of a frame (what is left is its content).
func stripT(raw []byte) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return json.RawMessage(raw)
	}
	delete(m, "t")
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(raw)
	}
	return b
}

type logMsg struct {
	rank int
	data json.RawMessage
}

func (h *Hub) onLogs(s *Session, records []json.RawMessage) {
	if len(records) == 0 {
		return
	}
	msgs := make([]logMsg, 0, len(records))
	for _, rec := range records {
		var lv struct {
			Level string `json:"level"`
		}
		_ = json.Unmarshal(rec, &lv)
		rank := levelRank(lv.Level)
		if rank == 0 {
			rank = levelRank("info")
		}
		data, err := json.Marshal(map[string]any{"session": s.ID, "ip": s.IP, "worker_id": s.WorkerID, "record": rec})
		if err != nil {
			continue
		}
		msgs = append(msgs, logMsg{rank: rank, data: data})
	}
	s.mu.Lock()
	s.logs = append(s.logs, msgs...)
	if len(s.logs) > sessionLogs {
		s.logs = append([]logMsg(nil), s.logs[len(s.logs)-sessionLogs:]...)
	}
	s.mu.Unlock()
	h.publishLogs(s, msgs)
}

// ipLimiter caps handshakes per address (§4.7: 30 a minute).
type ipLimiter struct {
	mu     sync.Mutex
	perMin int
	byIP   map[string]*ipBucket
	swept  time.Time
}

type ipBucket struct {
	l    *rate.Limiter
	last time.Time
}

func newIPLimiter(perMin int) *ipLimiter {
	return &ipLimiter{perMin: perMin, byIP: map[string]*ipBucket{}}
}

func (l *ipLimiter) allow(ip string, now time.Time) bool {
	if l == nil || l.perMin <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.swept) > 10*time.Minute {
		for k, b := range l.byIP {
			if now.Sub(b.last) > 10*time.Minute {
				delete(l.byIP, k)
			}
		}
		l.swept = now
	}
	b := l.byIP[ip]
	if b == nil {
		b = &ipBucket{l: rate.NewLimiter(rate.Limit(float64(l.perMin)/60), l.perMin)}
		l.byIP[ip] = b
	}
	b.last = now
	return b.l.AllowN(now, 1)
}

// CertIssuer signs a machine's cluster-link certificate (story 35.16).
type CertIssuer interface {
	IssueCert(ctx context.Context, nodeID, csrPEM string) (CertIssue, error)
}

// CertIssue is a signed certificate, the CA it chains to and the CA's fingerprint.
type CertIssue struct {
	Cert     string    `json:"cert"`
	CACert   string    `json:"ca_cert"`
	CASHA256 string    `json:"ca_sha256"`
	NotAfter time.Time `json:"not_after"`
}

// onCertCSR answers a machine's cert.csr (its key made on the machine; the session's
// v2 handshake proved both sides, so the CA returned on it is as trustworthy as a join's).
func (h *Hub) onCertCSR(s *Session, raw []byte) {
	var f struct {
		ID  string `json:"id"`
		CSR string `json:"csr"`
	}
	if json.Unmarshal(raw, &f) != nil {
		return
	}
	refused := func(reason string) {
		s.send(map[string]any{"t": "cert.refused", "id": f.ID, "reason": reason})
	}
	if s.Kind != "node" || s.NodeID == "" {
		refused("not a machine")
		return
	}
	if h.cfg.Certs == nil {
		refused("this home has no cluster CA")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		is, err := h.cfg.Certs.IssueCert(ctx, s.NodeID, f.CSR)
		if err != nil {
			h.log().Warn("cluster: a machine's certificate request was refused", "node_id", s.NodeID, "err", err,
				"event", "cluster_cert_refused")
			refused(err.Error())
			return
		}
		h.log().Info("cluster: certificate issued on a machine's channel session", "node_id", s.NodeID,
			"not_after", is.NotAfter, "event", "cluster_cert_issued")
		s.send(map[string]any{"t": "cert.issued", "id": f.ID, "cert": is.Cert, "ca_cert": is.CACert,
			"ca_sha256": is.CASHA256, "not_after": is.NotAfter})
	}()
}
