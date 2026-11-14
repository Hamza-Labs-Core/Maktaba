// Package relaydns keeps the relay pool's stable names in step with its healthy relays (Epic 50
// story 50.16 §4): relay.maktaba.tv and *.relay.maktaba.tv are DNS round robin over the healthy
// relays' public IPs. The witness runs it (cmd/relay-dns): every 15 s it reads the relays table
// and probes each relay's /readyz on its public IP, then adds the A records of relays that are
// healthy and answer, and removes the others, through the Cloudflare API, TTL 60 s. It never
// removes the last record: with every relay failing (or the witness unable to tell), the names
// keep the addresses they had, and forwarding plus standby tunnels keep homes reachable through
// whichever relays still answer.
package relaydns

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
)

// Defaults (story 50.16 §4).
const (
	Every = 15 * time.Second
	TTL   = 60
)

// Record is one A record.
type Record struct {
	ID      string
	Name    string
	Content string // the IP address
}

// DNS is the zone's A records.
type DNS interface {
	List(ctx context.Context, name string) ([]Record, error)
	Create(ctx context.Context, name, ip string, ttl int) error
	Delete(ctx context.Context, id string) error
}

// Cloudflare is DNS through the Cloudflare API (v4), scoped to one zone. The token needs Zone →
// DNS → Edit on that zone only.
type Cloudflare struct {
	BaseURL string // default https://api.cloudflare.com/client/v4
	ZoneID  string
	Token   string
	HTTP    *http.Client
}

func (c *Cloudflare) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return "https://api.cloudflare.com/client/v4"
}

func (c *Cloudflare) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

type cfRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl,omitempty"`
	Proxied bool   `json:"proxied"`
}

type cfReply struct {
	Success bool              `json:"success"`
	Errors  []json.RawMessage `json:"errors"`
	Result  json.RawMessage   `json:"result"`
}

func (c *Cloudflare) call(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+"/zones/"+url.PathEscape(c.ZoneID)+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var r cfReply
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return fmt.Errorf("relaydns: %s %s: %d, unreadable reply", method, path, resp.StatusCode)
	}
	if resp.StatusCode/100 != 2 || !r.Success {
		return fmt.Errorf("relaydns: %s %s: %d %s", method, path, resp.StatusCode, joinRaw(r.Errors))
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func joinRaw(rs []json.RawMessage) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = string(r)
	}
	return strings.Join(parts, "; ")
}

// List reads name's A records.
func (c *Cloudflare) List(ctx context.Context, name string) ([]Record, error) {
	var rs []cfRecord
	if err := c.call(ctx, http.MethodGet, "/dns_records?type=A&per_page=100&name="+url.QueryEscape(name), nil, &rs); err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(rs))
	for _, r := range rs {
		out = append(out, Record{ID: r.ID, Name: r.Name, Content: r.Content})
	}
	return out, nil
}

// Create adds an A record (DNS only: relayed video never goes through Cloudflare's proxy).
func (c *Cloudflare) Create(ctx context.Context, name, ip string, ttl int) error {
	return c.call(ctx, http.MethodPost, "/dns_records", cfRecord{Type: "A", Name: name, Content: ip, TTL: ttl}, nil)
}

// Delete removes a record by id.
func (c *Cloudflare) Delete(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, "/dns_records/"+url.PathEscape(id), nil, nil)
}

// Controller reconciles the stable names with the healthy relays.
type Controller struct {
	Relays relay.PoolStore
	DNS    DNS
	// Names are the round-robin names (relay.maktaba.tv, *.relay.maktaba.tv).
	Names []string
	TTL   int // default TTL
	// Probe reports whether a relay answers on its public IP (ProbeReadyz); nil: every healthy
	// relay counts as answering.
	Probe  func(ctx context.Context, r relay.PoolRelay) bool
	Logger *slog.Logger
}

func (c *Controller) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// Result is what one Reconcile did.
type Result struct {
	Want    []string // the IPs the names should hold
	Added   int
	Removed int
	Kept    bool // nothing healthy: the records were kept as they were
}

// ErrNoRelays: the relays table could not be read; nothing was changed.
var ErrNoRelays = errors.New("relaydns: the relays table could not be read; the records are kept")

// Reconcile does one round: each name gains the A records of the relays healthy and answering,
// then loses the others; with none healthy it changes nothing (never the last record).
func (c *Controller) Reconcile(ctx context.Context) (Result, error) {
	var res Result
	rs, err := c.Relays.Relays(ctx)
	if err != nil {
		return res, fmt.Errorf("%w: %v", ErrNoRelays, err)
	}
	want := map[string]bool{}
	for _, r := range rs {
		if !r.Healthy() || r.PublicIP == "" {
			continue
		}
		if c.Probe != nil && !c.Probe(ctx, r) {
			c.logger().Warn("relay-dns: a relay heartbeats but does not answer on its public IP", "relay", r.ID, "event", "relay_dns_probe_failed")
			continue
		}
		want[r.PublicIP] = true
	}
	for ip := range want {
		res.Want = append(res.Want, ip)
	}
	sort.Strings(res.Want)
	if len(want) == 0 {
		res.Kept = true
		c.logger().Warn("relay-dns: no relay is healthy; the records are kept as they are", "event", "relay_dns_kept")
		return res, nil
	}
	ttl := c.TTL
	if ttl <= 0 {
		ttl = TTL
	}
	var errs []error
	for _, name := range c.Names {
		have, err := c.DNS.List(ctx, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		got := map[string]bool{}
		for _, r := range have {
			got[r.Content] = true
		}
		// add first: a name is never left with no record between the two steps
		added := 0
		for _, ip := range res.Want {
			if got[ip] {
				continue
			}
			if err := c.DNS.Create(ctx, name, ip, ttl); err != nil {
				errs = append(errs, err)
				continue
			}
			added++
			c.logger().Info("relay-dns: a relay added to the round robin", "name", name, "ip", ip, "event", "relay_dns_added")
		}
		res.Added += added
		if added < len(res.Want)-countIn(res.Want, got) {
			continue // an add failed: remove nothing this round
		}
		for _, r := range have {
			if want[r.Content] {
				continue
			}
			if err := c.DNS.Delete(ctx, r.ID); err != nil {
				errs = append(errs, err)
				continue
			}
			res.Removed++
			c.logger().Info("relay-dns: a relay removed from the round robin", "name", name, "ip", r.Content, "event", "relay_dns_removed")
		}
	}
	return res, errors.Join(errs...)
}

func countIn(ips []string, set map[string]bool) int {
	n := 0
	for _, ip := range ips {
		if set[ip] {
			n++
		}
	}
	return n
}

// Run reconciles every every (default Every) until ctx ends.
func (c *Controller) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = Every
	}
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		rctx, cancel := context.WithTimeout(ctx, every)
		if _, err := c.Reconcile(rctx); err != nil {
			c.logger().Warn("relay-dns: a round failed", "err", err, "event", "relay_dns_round_failed")
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}

// ProbeReadyz probes a relay as a client would reach it: https://<host>/readyz with the
// connection made to the relay's public IP on port, the certificate checked for host.
func ProbeReadyz(host string, port int, timeout time.Duration) func(context.Context, relay.PoolRelay) bool {
	return probeReadyz(host, port, timeout, nil)
}

// probeReadyz is ProbeReadyz trusting roots (nil: the system's).
func probeReadyz(host string, port int, timeout time.Duration, roots *x509.CertPool) func(context.Context, relay.PoolRelay) bool {
	return func(ctx context.Context, r relay.PoolRelay) bool {
		d := &net.Dialer{Timeout: timeout}
		tr := &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return d.DialContext(ctx, network, net.JoinHostPort(r.PublicIP, fmt.Sprint(port)))
			},
			TLSClientConfig:   &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: roots},
			DisableKeepAlives: true,
		}
		defer tr.CloseIdleConnections()
		cl := &http.Client{Transport: tr, Timeout: timeout}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/readyz", nil)
		if err != nil {
			return false
		}
		resp, err := cl.Do(req)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
}
