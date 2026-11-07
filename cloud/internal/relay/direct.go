package relay

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/direct"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Direct remote access's frames (Epic 50 story 50.15), sent only on a tunnel whose home announced
// FeatureDirect in AUTH and whose relay answered it:
//
//	0x40  META_ENDPOINTS   home → cloud  JSON EndpointsPayload: its direct listener's port and state
//	0x41  DIRECT_CSR       home → cloud  JSON CSRPayload: a CSR for *.<home id>.<its domain>
//	0x42  DIRECT_CERT      cloud → home  JSON CertPayload: the chain issued
//	0x43  DIRECT_CHECK     cloud → home  JSON CheckPayload: the outside check's result
//	0x44  DIRECT_CHECK_NOW home → cloud  empty: the admin pressed Check now
const (
	KindMetaEndpoints  byte = 0x40
	KindDirectCSR      byte = 0x41
	KindDirectCert     byte = 0x42
	KindDirectCheck    byte = 0x43
	KindDirectCheckNow byte = 0x44
)

// FeatureDirect: the home speaks the direct frames.
const FeatureDirect = "direct"

// EndpointsPayload is the home's direct listener as it reports it. The public address is the
// one the relay sees the tunnel come from, not one the home claims.
type EndpointsPayload struct {
	Port     int      `json:"port"`               // the external port the router maps (or the admin forwarded)
	State    string   `json:"state"`              // on, off, cgnat, mapping_failed
	LAN      []string `json:"lan,omitempty"`      // the listener's LAN addresses (ip:port)
	External string   `json:"external,omitempty"` // the router's reported external address
}

// CSRPayload carries a CSR (DER); the key stays on the home.
type CSRPayload struct {
	CSR []byte `json:"csr"`
}

// CertPayload carries the chain issued for the home, with its names' domain and home id.
type CertPayload struct {
	Chain  string `json:"chain"`
	Domain string `json:"domain"`
	HomeID string `json:"home_id"`
}

// CheckPayload is the outside check's result.
type CheckPayload struct {
	Result string    `json:"result"`
	Port   int       `json:"port"`
	At     time.Time `json:"at"`
}

// DirectHandler keeps what homes send and gives what they are owed (DBDirect).
type DirectHandler interface {
	Endpoints(ctx context.Context, serverID string, from netip.Addr, p EndpointsPayload) error
	CSR(ctx context.Context, serverID string, csr []byte) error
	CheckNow(ctx context.Context, serverID string) error
	// Deliveries are the frames owed to the home now (a new chain, a changed result).
	Deliveries(ctx context.Context, serverID string) ([]Frame, error)
}

// WithDirect turns the direct frames on for a tunnel from from.
func WithDirect(h DirectHandler, from netip.Addr, logger *slog.Logger) TunnelOption {
	return func(t *Tunnel) { t.direct, t.from, t.logger = h, from, logger }
}

// onDirect handles one direct frame from the home, then sends what it is owed.
func (t *Tunnel) onDirect(f Frame) {
	if t.direct == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var err error
	switch f.Kind {
	case KindMetaEndpoints:
		var p EndpointsPayload
		if err = json.Unmarshal(f.Payload, &p); err == nil {
			err = t.direct.Endpoints(ctx, t.ServerID, t.from, p)
		}
	case KindDirectCSR:
		var p CSRPayload
		if err = json.Unmarshal(f.Payload, &p); err == nil {
			err = t.direct.CSR(ctx, t.ServerID, p.CSR)
		}
	case KindDirectCheckNow:
		err = t.direct.CheckNow(ctx, t.ServerID)
	}
	if err != nil && t.logger != nil {
		t.logger.Warn("relay: a direct frame failed", "server_id", t.ServerID, "kind", f.Kind, "err", err, "event", "direct_frame_failed")
	}
	t.deliverDirect(ctx)
}

// deliverDirect sends the frames the home is owed.
func (t *Tunnel) deliverDirect(ctx context.Context) {
	if t.direct == nil || t.closed.Load() {
		return
	}
	frames, err := t.direct.Deliveries(ctx, t.ServerID)
	if err != nil {
		return
	}
	for _, f := range frames {
		if err := t.writeFrame(f); err != nil {
			return
		}
	}
}

// keepDelivering looks for what the home is owed every every (the worker's chains and checks).
func (t *Tunnel) keepDelivering(every time.Duration) {
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-tk.C:
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			t.deliverDirect(ctx)
			cancel()
		}
	}
}

// tunnelSource is the address the tunnel comes from: the peer, or behind a proxy on this host or
// network (Caddy) the rightmost X-Forwarded-For entry; entries further left are the client's own.
func tunnelSource(r *http.Request) netip.Addr {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(peer)
	if err != nil {
		return netip.Addr{}
	}
	ip = ip.Unmap()
	if !ip.IsLoopback() && !ip.IsPrivate() {
		return ip
	}
	vs := r.Header.Values("X-Forwarded-For")
	if len(vs) == 0 {
		return ip
	}
	parts := strings.Split(vs[len(vs)-1], ",")
	if last, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1])); err == nil {
		return last.Unmap()
	}
	return ip
}

// DBDirect is the DirectHandler on Postgres (stores.Direct). Domains is read on every new
// home, so a domain appended to the configuration is used at once.
type DBDirect struct {
	Store    *stores.Direct
	Domains  func() []string
	Budget   int
	Logger   *slog.Logger
	Overflow func(domain, ca string) // the week's budgets were all used (an alert)
	Now      func() time.Time
}

func (d *DBDirect) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Endpoints records the report; a router reporting a shared address (CGNAT) is the home's own
// cgnat state.
func (d *DBDirect) Endpoints(ctx context.Context, serverID string, from netip.Addr, p EndpointsPayload) error {
	state := p.State
	if ext, err := netip.ParseAddr(p.External); err == nil && state == "on" && direct.Shared(ext) {
		state = direct.CheckCGNAT
	}
	if state == "on" && (p.Port <= 0 || p.Port > 65535 || !from.IsValid() || direct.Shared(from)) {
		state = "off"
	}
	return d.Store.SetEndpoints(ctx, serverID, from, p.Port, state)
}

// CSR queues the home's CSR for the worker; a new home gets its domain and CA here.
func (d *DBDirect) CSR(ctx context.Context, serverID string, csr []byte) error {
	if len(csr) == 0 || len(csr) > 16<<10 {
		return errBadCSR
	}
	budget := d.Budget
	if budget <= 0 {
		budget = direct.WeeklyBudget
	}
	c, overflow, err := d.Store.PutCSR(ctx, serverID, csr, func(use []direct.DomainUse) (string, string, bool) {
		return direct.Pick(d.Domains(), use, budget)
	}, d.now())
	if err == nil && overflow && d.Overflow != nil {
		d.Overflow(c.Domain, c.CA)
	}
	return err
}

// CheckNow asks the worker to check the home now.
func (d *DBDirect) CheckNow(ctx context.Context, serverID string) error {
	return d.Store.RequestCheck(ctx, serverID)
}

// Deliveries are a new chain and a changed check result.
func (d *DBDirect) Deliveries(ctx context.Context, serverID string) ([]Frame, error) {
	var out []Frame
	chain, domain, err := d.Store.CertToDeliver(ctx, serverID)
	if err != nil {
		return nil, err
	}
	if chain != "" {
		b, _ := json.Marshal(CertPayload{Chain: chain, Domain: domain, HomeID: direct.HomeID(serverID)})
		out = append(out, Frame{Kind: KindDirectCert, Payload: b})
	}
	result, port, at, err := d.Store.CheckToDeliver(ctx, serverID)
	if err != nil {
		return out, err
	}
	if result != "" {
		b, _ := json.Marshal(CheckPayload{Result: result, Port: port, At: at.UTC()})
		out = append(out, Frame{Kind: KindDirectCheck, Payload: b})
	}
	return out, nil
}

type relayError string

func (e relayError) Error() string { return string(e) }

const errBadCSR = relayError("relay: a CSR of no size or too big")
