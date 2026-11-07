package plugins

// The service route (Epic 54, story 54.35): a first-party plugin's stage reaches Plex
// through the home's service door, which adds the secret and scrubs it out of the answer,
// so the plugin never holds the token or the server's address.
//
//	trusted/service  {service, method, target, accept, content_type, body (base64), timeout_ms}
//	                 -> {status, content_type, body (base64)}
//
// The call must name a service the plugin's manifest lists (`[trusted] service`, else 403
// plugin_capability), and its method and target must be on the door's allow-list for a
// caller on the home (else 403 plugin_service_path). Only the home serves it: a joined
// machine's plugin API has no channel to the home's door, so a stage there asks its worker,
// which forwards through its agent channel (pipeline: plugins/native.py, service_request).

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"slices"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/relay"
)

// CodeServicePath: the call is not on the service's allow-list.
const CodeServicePath = "plugin_service_path"

// ServiceDoor is the home's service door (relay.Door).
type ServiceDoor interface {
	Serve(ctx context.Context, c relay.Caller, req relay.Request, sink relay.Sink) relay.Result
}

type serviceRequest struct {
	Service     string `json:"service"`
	Method      string `json:"method"`
	Target      string `json:"target"`
	Accept      string `json:"accept"`
	ContentType string `json:"content_type"`
	Body        string `json:"body"`
	TimeoutMS   int64  `json:"timeout_ms"`
}

type serviceAnswer struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Body        string `json:"body"`
}

type memSink struct{ buf bytes.Buffer }

func (m *memSink) Chunk(_ context.Context, d []byte) error {
	m.buf.Write(d)
	return nil
}

// maxServiceTimeout bounds what a call may ask (the door's own limits apply too).
const maxServiceTimeout = 120 * time.Second

func (s *Server) serviceRoute() {
	s.routes["trusted/service"] = func(c *call) (any, *Refusal) {
		var req serviceRequest
		if ref := c.decode(&req); ref != nil {
			return nil, ref
		}
		if c.inv.FirstParty == nil || !slices.Contains(c.inv.FirstParty.Trusted.Service, req.Service) {
			return nil, &Refusal{Status: http.StatusForbidden, Code: CodeCapability,
				Detail: "the plugin's manifest does not list the service " + clipText(req.Service, 40)}
		}
		if s.Service == nil {
			return nil, &Refusal{Status: http.StatusServiceUnavailable, Code: relay.CodeOff,
				Detail: "this machine's plugin API has no service door (a joined machine's stage asks its worker)"}
		}
		body, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return nil, &Refusal{Status: http.StatusBadRequest, Code: CodeInvalid, Detail: "body is not base64"}
		}
		timeout := time.Duration(req.TimeoutMS) * time.Millisecond
		if timeout <= 0 || timeout > maxServiceTimeout {
			timeout = 30 * time.Second
		}
		sink := &memSink{}
		res := s.Service.Serve(c.ctx, relay.Caller{Kind: "home", WorkerID: c.inv.PluginID, Session: c.inv.ID},
			relay.Request{Ext: req.Service, Method: req.Method, Target: req.Target, Accept: req.Accept,
				ContentType: req.ContentType, Body: body, Timeout: timeout}, sink)
		if res.Err != nil {
			switch res.Err.Code {
			case relay.CodeNotAllowed, relay.CodeBadRequest:
				return nil, &Refusal{Status: http.StatusForbidden, Code: CodeServicePath, Detail: res.Err.Detail}
			case relay.CodeRateLimited, relay.CodeBusy:
				return nil, &Refusal{Status: http.StatusTooManyRequests, Code: res.Err.Code, Detail: res.Err.Detail}
			default:
				return nil, &Refusal{Status: http.StatusBadGateway, Code: res.Err.Code, Detail: res.Err.Detail}
			}
		}
		return serviceAnswer{Status: res.Status, ContentType: res.ContentType,
			Body: base64.StdEncoding.EncodeToString(sink.buf.Bytes())}, nil
	}
}

func clipText(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
