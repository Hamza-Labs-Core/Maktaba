package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginmanifest"
)

// Rate limits of one invocation (54.04 §5, README §4.7).
const (
	RatePerSecond = 200.0
	RateBurst     = 200.0
	// MaxBody is the largest request a plugin call may send (an artifact, base64).
	MaxBody = 6 << 20
)

// Server serves /plugin-api/v1/<interface>/<function> to maktaba-plugind. It is mounted
// only on the unix socket (ListenUnix), never on the TCP API (54.04 §1).
type Server struct {
	DB     *sql.DB
	Tokens *Tokens
	Logger *slog.Logger
	// Media reads video files and decodes frames and audio (54.05).
	Media *Media
	// ArtifactsDir is <data>/plugins/artifacts: subtitles and artwork plugins write.
	ArtifactsDir string
	// Compute answers compute.embed and compute.llm (54.21; nil: unavailable).
	Compute *Compute
	routes  map[string]route
	once    sync.Once
}

// call is one plugin call, checked: its invocation and its JSON body.
type call struct {
	ctx  context.Context
	inv  *Invocation
	body json.RawMessage
	s    *Server
}

type route func(c *call) (any, *Refusal)

// binary is an answer sent as application/octet-stream (media reads, frames, audio).
type binary []byte

var (
	metricsOnce sync.Once
	refusals    *prometheus.CounterVec
	callsTotal  *prometheus.CounterVec
)

func metrics() {
	metricsOnce.Do(func() {
		refusals = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "maktaba_plugin_refusals_total",
			Help: "Plugin API calls refused, by plugin and code (Epic 54)."}, []string{"plugin", "code"})
		callsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "maktaba_plugin_api_calls_total",
			Help: "Plugin API calls, by plugin and route (Epic 54)."}, []string{"plugin", "route"})
		_ = prometheus.DefaultRegisterer.Register(refusals)
		_ = prometheus.DefaultRegisterer.Register(callsTotal)
	})
}

// capabilityOf answers the capabilities that unlock interface/function ("" none needed),
// from capabilities.toml. media/info answers to library.read (54.05 §1).
func capabilityOf(iface, fn string) []string {
	switch iface {
	case "job", "log", "data":
		return nil
	}
	if iface == "media" && fn == "info" {
		return []string{"library.read"}
	}
	var out []string
	for _, c := range pluginmanifest.Capabilities() {
		if c.Interface != iface {
			continue
		}
		for _, f := range c.Functions {
			if f == "*" || f == fn {
				out = append(out, c.Name)
			}
		}
	}
	if out == nil {
		out = []string{"-"} // a gated interface with no such function: never granted
	}
	return out
}

func (s *Server) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Server) init() {
	s.once.Do(func() {
		metrics()
		s.routes = map[string]route{}
		s.dataRoutes()
		s.libraryRoutes()
		s.writeRoutes()
		s.otherRoutes()
		s.mediaRoutes()
		s.computeRoutes()
	})
}

// ServeHTTP is the plugin API's whole handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.init()
	rest, ok := strings.CutPrefix(r.URL.Path, "/plugin-api/v1/")
	if !ok || r.Method != http.MethodPost {
		writeRefusal(w, refuse(404, CodeNotFound, "no such route"))
		return
	}
	iface, fn, _ := strings.Cut(rest, "/")
	auth := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(auth, "Plugin ")
	if !ok {
		writeRefusal(w, refuse(401, CodeToken, "Authorization: Plugin <token>"))
		return
	}
	inv, ref := s.Tokens.Check(r.Context(), token)
	if ref != nil {
		writeRefusal(w, ref)
		return
	}
	rt := s.routes[iface+"/"+fn]
	if rt == nil {
		s.refused(w, inv, iface+"/"+fn, refuse(404, CodeNotFound, "no such route"))
		return
	}
	callsTotal.WithLabelValues(inv.PluginID, iface+"/"+fn).Inc()
	if !inv.takeRate(s.Tokens.Now()) {
		s.refused(w, inv, iface+"/"+fn, refuse(403, CodeRate, "over 200 calls a second"))
		return
	}
	if need := capabilityOf(iface, fn); need != nil {
		granted := false
		for _, c := range need {
			if inv.Granted(c) {
				granted = true
			}
		}
		if !granted {
			s.refused(w, inv, iface+"/"+fn, refuse(403, CodeCapability, strings.Join(need, " or ")))
			return
		}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil {
		writeRefusal(w, refuse(400, CodeInvalid, err.Error()))
		return
	}
	if len(body) > MaxBody {
		s.refused(w, inv, iface+"/"+fn, refuse(413, CodeTooLarge, "request over 6 MB"))
		return
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	out, ref := rt(&call{ctx: r.Context(), inv: inv, body: body, s: s})
	if ref != nil {
		s.refused(w, inv, iface+"/"+fn, ref)
		return
	}
	switch v := out.(type) {
	case binary:
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(v)
	default:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
}

// refused logs and counts a refusal, then answers it (54.04 §5).
func (s *Server) refused(w http.ResponseWriter, inv *Invocation, route string, ref *Refusal) {
	refusals.WithLabelValues(inv.PluginID, ref.Code).Inc()
	inv.mu.Lock()
	inv.refusalsTotal++
	inv.mu.Unlock()
	s.logger().Warn("plugin_call_refused", "plugin", inv.PluginID, "job_id", inv.JobID, "route", route, "code", ref.Code)
	writeRefusal(w, ref)
}

func writeRefusal(w http.ResponseWriter, ref *Refusal) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ref.Status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": ref.Code})
}

// takeRate spends one call from the invocation's token bucket.
func (inv *Invocation) takeRate(now time.Time) bool {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.bucket += now.Sub(inv.bucketAt).Seconds() * RatePerSecond
	if inv.bucket > RateBurst {
		inv.bucket = RateBurst
	}
	inv.bucketAt = now
	if inv.bucket < 1 {
		return false
	}
	inv.bucket--
	return true
}

func (c *call) decode(v any) *Refusal {
	if err := json.Unmarshal(c.body, v); err != nil {
		return refuse(400, CodeInvalid, err.Error())
	}
	return nil
}

// ListenUnix serves the plugin API on a unix socket only (mode 0660), until ctx ends.
func ListenUnix(ctx context.Context, path string, s *Server) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	_ = os.Chmod(path, 0o660)
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	err = srv.Serve(l)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
