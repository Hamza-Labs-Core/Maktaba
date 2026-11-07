package directaccess

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"sync"
	"time"

	"github.com/jackpal/gateway"
)

// ServerIDHeader is how the direct listener says which home it is: /api/health answers it with
// the home's cloud server_id, and the cloud's outside check compares it.
const ServerIDHeader = "X-Maktaba-Server-Id"

// ViaHeader marks a request that came in on the direct listener (the internet), as the relay
// agent marks its own with "relay": the API refuses the machines' routes to it and never takes
// it for a program on this machine.
const ViaHeader = "X-Maktaba-Via"

// States the home reports.
const (
	StateOn            = "on"
	StateOff           = "off"
	StateCGNAT         = "cgnat"
	StateMappingFailed = "mapping_failed"
)

// Settings are rules.direct.
type Settings struct {
	Enabled    bool
	MapPort    bool
	ManualPort int
}

// Status is what the home's settings show.
type Status struct {
	Enabled    bool      `json:"enabled"`
	State      string    `json:"state"`
	Port       int       `json:"port,omitempty"`     // the public port (mapped or forwarded by hand)
	Internal   int       `json:"internal,omitempty"` // the direct listener's port on this machine
	Listening  bool      `json:"listening"`
	By         string    `json:"by,omitempty"` // upnp, natpmp, pcp, manual
	ExternalIP string    `json:"external_ip,omitempty"`
	Error      string    `json:"error,omitempty"`
	Name       string    `json:"name,omitempty"` // *.<home id>.<domain>
	NotAfter   time.Time `json:"not_after,omitzero"`
	Check      *Check    `json:"check,omitempty"`
	// Online: the relay agent has a tunnel to a cloud with direct access.
	Online bool `json:"online"`
}

// Service is the home's direct access: three loops (RunListener, RunPortmap, RunCert) and
// what the settings page reads.
type Service struct {
	Files    *Files
	Agent    *Agent
	Listen   string   // the direct listener (toml direct.listen, default :8444)
	Upstream *url.URL // this machine's web port on loopback (the same pages and API as the LAN)
	// ServerID is the home's cloud server id ("" not claimed yet).
	ServerID func() string
	Settings func(ctx context.Context) (Settings, error)
	// Mappers are asked in order; nil: UPnP-IGD, then NAT-PMP and PCP at the default gateway.
	Mappers []Mapper
	// LAN lists the listener's LAN addresses (ip:port); nil: this machine's private IPv4s.
	LAN  func(port int) []string
	Log  *slog.Logger
	Poll time.Duration // how often the settings are read again; default 30 s
	Now  func() time.Time

	mu        sync.Mutex
	st        Status
	mappersOK bool
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) poll() time.Duration {
	if s.Poll > 0 {
		return s.Poll
	}
	return 30 * time.Second
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) settings(ctx context.Context) Settings {
	if s.Settings == nil {
		return Settings{}
	}
	st, err := s.Settings(ctx)
	if err != nil {
		s.log().Warn("direct: reading rules.direct", "err", err)
		return Settings{}
	}
	return st
}

func (s *Service) update(fn func(*Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.st)
}

// Status is the settings page's view.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st
	if st.Check != nil {
		c := *st.Check
		st.Check = &c
	}
	return st
}

// internalPort is the direct listener's port as configured (direct.listen, host:port).
func (s *Service) internalPort() (int, error) {
	_, p, err := net.SplitHostPort(s.Listen)
	if err != nil {
		return 0, fmt.Errorf("directaccess: direct.listen %q: %w", s.Listen, err)
	}
	return ParsePort(p)
}

// sleep waits d or until ctx ends (false).
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// ---- the listener -------------------------------------------------------------------------

// RunListener serves the direct listener while direct access is on (loop direct_listener). It
// presents the home's certificate; until there is one, handshakes fail.
func (s *Service) RunListener(ctx context.Context) {
	var srv *http.Server
	var done chan struct{}
	stop := func() {
		if srv == nil {
			return
		}
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = srv.Shutdown(sctx)
		cancel()
		<-done
		srv, done = nil, nil
		s.update(func(st *Status) { st.Listening = false })
	}
	defer stop()
	for {
		on := s.settings(ctx).Enabled
		switch {
		case on && srv == nil:
			ln, err := net.Listen("tcp", s.Listen)
			if err != nil {
				s.log().Warn("direct: the listener", "addr", s.Listen, "err", err, "event", "direct_listen_failed")
				s.update(func(st *Status) { st.Error = err.Error() })
				break
			}
			port := ln.Addr().(*net.TCPAddr).Port
			srv = &http.Server{
				Handler:           s.Handler(),
				ReadHeaderTimeout: 10 * time.Second,
				IdleTimeout:       2 * time.Minute,
				TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
					if c := s.Files.Cert(); c != nil {
						return c, nil
					}
					return nil, errors.New("direct: no certificate yet")
				}},
			}
			done = make(chan struct{})
			go func(srv *http.Server, done chan struct{}) { // not a loop: this loop's server, stopped above
				defer close(done)
				_ = srv.ServeTLS(ln, "", "")
			}(srv, done)
			s.update(func(st *Status) { st.Listening, st.Internal = true, port })
			s.log().Info("direct: listening", "addr", ln.Addr().String(), "event", "direct_listening")
		case !on && srv != nil:
			stop()
		}
		if !sleep(ctx, s.poll()) {
			return
		}
	}
}

// Handler is the direct listener's handler: this machine's web port, as a request from the
// internet (ViaHeader direct, forwarding headers the client sent dropped), with /api/health
// naming the home.
func (s *Service) Handler() http.Handler {
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(s.Upstream)
			pr.Out.Host = pr.In.Host
			for _, h := range []string{"Forwarded", "X-Real-Ip", "True-Client-Ip", ViaHeader} {
				pr.Out.Header.Del(h)
			}
			pr.SetXForwarded()
			pr.Out.Header.Set(ViaHeader, "direct")
		},
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			if resp.Request != nil && resp.Request.URL.Path == "/api/health" && s.ServerID != nil {
				if id := s.ServerID(); id != "" {
					resp.Header.Set(ServerIDHeader, id)
				}
			}
			return nil
		},
		ErrorLog: slog.NewLogLogger(s.log().Handler(), slog.LevelWarn),
	}
	return rp
}

// ---- the port on the router ---------------------------------------------------------------

func (s *Service) mappers() []Mapper {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mappersOK {
		return s.Mappers
	}
	s.mappersOK = true
	if s.Mappers == nil {
		s.Mappers = []Mapper{&UPnP{}}
		if gw, err := gateway.DiscoverGateway(); err == nil {
			if a, ok := netip.AddrFromSlice(gw); ok && a.Unmap().Is4() {
				ap := netip.AddrPortFrom(a.Unmap(), 5351)
				s.Mappers = append(s.Mappers, &NATPMP{Gateway: ap}, &PCP{Gateway: ap})
			}
		}
	}
	return s.Mappers
}

// RunPortmap keeps the port open on the router while direct access is on (loop
// direct_portmap): at start and every 20 minutes, sooner when the settings change, and tells
// the cloud the result. Stopping it removes the mapping and tells the cloud direct is off.
func (s *Service) RunPortmap(ctx context.Context) {
	var held *Mapping
	var by Mapper
	var last Settings
	var next time.Time
	first := true
	internal, portErr := s.internalPort()
	if portErr != nil {
		s.log().Error("direct: no port to map", "err", portErr, "event", "direct_bad_listen")
	}
	unmap := func() {
		if held == nil {
			return
		}
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := by.Unmap(uctx, *held); err != nil {
			s.log().Warn("direct: removing the port mapping", "by", by.Name(), "port", held.External, "err", err)
		}
		cancel()
		held, by = nil, nil
	}
	defer func() {
		unmap()
		rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.report(rctx, Endpoints{State: StateOff})
		cancel()
		s.update(func(st *Status) { st.Enabled, st.State, st.Port, st.By = false, StateOff, 0, "" })
	}()
	for {
		cur := s.settings(ctx)
		if first || cur != last || !s.now().Before(next) {
			first, last = false, cur
			ep := Endpoints{Port: 0, State: StateOff}
			errText := ""
			switch {
			case !cur.Enabled:
				unmap()
				next = s.now().Add(RenewEvery)
			case cur.MapPort && portErr != nil:
				unmap()
				ep.State, errText = StateMappingFailed, portErr.Error()
				next = s.now().Add(RenewEvery)
			case cur.MapPort:
				want := internal
				if held != nil {
					want = held.External
				}
				var m Mapping
				var err error
				if by != nil {
					m, err = by.Map(ctx, internal, want, Lease)
				}
				if by == nil || err != nil {
					m, by, err = MapFirst(ctx, s.mappers(), internal, want)
				}
				if err != nil {
					held, by = nil, nil
					ep.State, errText = StateMappingFailed, err.Error()
					next = s.now().Add(5 * time.Minute)
					s.log().Warn("direct: the router opened no port", "err", err, "event", "direct_mapping_failed")
					break
				}
				held = &m
				next = s.now().Add(RenewEvery)
				ep.Port, ep.State = m.External, StateOn
				if m.ExternalIP.IsValid() {
					ep.External = m.ExternalIP.String()
					if Shared(m.ExternalIP) {
						ep.State = StateCGNAT
					}
				}
			default:
				unmap()
				next = s.now().Add(RenewEvery)
				if cur.ManualPort > 0 {
					ep.Port, ep.State = cur.ManualPort, StateOn
				}
			}
			if ep.State != StateOff {
				ep.LAN = s.lan(internal)
			}
			s.report(ctx, ep)
			via := ""
			if held != nil && by != nil {
				via = by.Name()
			} else if cur.Enabled && !cur.MapPort && cur.ManualPort > 0 {
				via = "manual"
			}
			s.update(func(st *Status) {
				st.Enabled, st.State, st.Port, st.By, st.ExternalIP, st.Error = cur.Enabled, ep.State, ep.Port, via, ep.External, errText
				if st.Internal == 0 {
					st.Internal = internal
				}
			})
		}
		if !sleep(ctx, s.poll()) {
			return
		}
	}
}

func (s *Service) report(ctx context.Context, ep Endpoints) {
	if s.Agent == nil {
		return
	}
	if err := s.Agent.Post(ctx, AgentRequest{Endpoints: &ep}); err != nil {
		s.log().Warn("direct: telling the cloud the endpoints", "err", err)
	}
}

func (s *Service) lan(port int) []string {
	if s.LAN != nil {
		return s.LAN(port)
	}
	pv, err := portValue(port, false)
	if err != nil {
		return nil
	}
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		p, err := netip.ParsePrefix(a.String())
		if err != nil {
			continue
		}
		if ip := p.Addr(); ip.Is4() && ip.IsPrivate() {
			out = append(out, netip.AddrPortFrom(ip, pv).String())
		}
	}
	return out
}

// ---- the certificate ----------------------------------------------------------------------

// RunCert keeps the home's certificate while direct access is on (loop direct_cert): it asks
// the cloud its name, sends a CSR for it (the key stays here) and keeps the chain the cloud
// returns, renewing at two thirds of its life; it keeps the outside check's result.
func (s *Service) RunCert(ctx context.Context) {
	var lastCSR time.Time
	for {
		if s.settings(ctx).Enabled && s.Agent != nil {
			lastCSR = s.certOnce(ctx, lastCSR)
		}
		if !sleep(ctx, s.poll()) {
			return
		}
	}
}

func (s *Service) certOnce(ctx context.Context, lastCSR time.Time) time.Time {
	st, err := s.Agent.Status(ctx)
	if err != nil {
		s.update(func(x *Status) { x.Online = false })
		return lastCSR
	}
	n := s.Files.Name()
	if st.Domain != "" && st.HomeID != "" && (Name{Domain: st.Domain, HomeID: st.HomeID}) != n {
		n = Name{Domain: st.Domain, HomeID: st.HomeID}
		if err := s.Files.SetName(n); err != nil {
			s.log().Warn("direct: keeping the name", "err", err)
		}
	}
	if st.Chain != "" && n.Domain != "" {
		cur := s.Files.Cert()
		if cur == nil || !bytes.Equal(firstDER(st.Chain), cur.Certificate[0]) {
			if err := s.Files.SetChain([]byte(st.Chain), n); err != nil {
				s.log().Warn("direct: a chain refused", "err", err, "event", "direct_chain_refused")
			} else {
				s.log().Info("direct: a new certificate", "name", n.Wildcard(), "event", "direct_cert_installed")
			}
		}
	}
	switch {
	case n.Domain == "":
		if err := s.Agent.Post(ctx, AgentRequest{AskName: true}); err != nil {
			s.log().Warn("direct: asking the name", "err", err)
		}
	case RenewDue(s.Files.Cert(), n, s.now()) && !st.Pending && s.now().Sub(lastCSR) >= time.Hour:
		csr, err := s.Files.CSR(n)
		if err == nil {
			err = s.Agent.Post(ctx, AgentRequest{CSR: csr})
		}
		if err != nil {
			s.log().Warn("direct: sending the CSR", "err", err)
		} else {
			lastCSR = s.now()
		}
	}
	c := s.Files.Cert()
	s.update(func(x *Status) {
		x.Online, x.Check = st.Online, st.Check
		if n.Domain != "" {
			x.Name = n.Wildcard()
		}
		if c != nil && c.Leaf != nil {
			x.NotAfter = c.Leaf.NotAfter
		}
	})
	return lastCSR
}

// CheckNow asks the cloud to check the home from outside now.
func (s *Service) CheckNow(ctx context.Context) error {
	if s.Agent == nil {
		return errors.New("direct: no relay agent")
	}
	return s.Agent.Post(ctx, AgentRequest{CheckNow: true})
}
