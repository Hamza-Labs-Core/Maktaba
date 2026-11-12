package liveguard

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/clientip"
)

// HDHRAllowedKey is the app_settings key of the admin's list of addresses that may use
// the tuner (api/internal/handlers/tv.HDHRAllowedKey): {"addresses": ["192.168.1.20", …]}.
const HDHRAllowedKey = "tv.hdhr_allowed"

// TunerTTL is how long the loaded list is reused: an address an admin removes stops
// within it.
const TunerTTL = 20 * time.Second

// Tuner is the addresses that may use the tuner (the HDHomeRun routes, the guide
// exports and a channel's stream with no viewer named): this machine, the Plex server
// set up in Maktaba, and the admin's list. Anything it cannot load allows nothing
// beyond this machine.
type Tuner struct {
	// Load reads the allowed prefixes besides loopback.
	Load   func(ctx context.Context) ([]netip.Prefix, error)
	Now    func() time.Time
	Logger *slog.Logger

	mu     sync.Mutex
	loaded []netip.Prefix
	until  time.Time
}

// Allowed reports whether addr may use the tuner.
func (t *Tuner) Allowed(ctx context.Context, addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	if addr.IsLoopback() {
		return true
	}
	if t == nil || t.Load == nil {
		return false
	}
	now := time.Now()
	if t.Now != nil {
		now = t.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !now.Before(t.until) {
		ps, err := t.Load(ctx)
		if err != nil {
			// never the list as it was: an address removed since would stay allowed
			if t.Logger != nil && !errors.Is(err, context.Canceled) {
				t.Logger.Warn("tv: the tuner's allowed addresses could not be read; only this machine is allowed",
					"err", err.Error(), "event", "tv_tuner_allowed_failed")
			}
			t.loaded, t.until = nil, now.Add(2*time.Second)
		} else {
			t.loaded, t.until = ps, now.Add(TunerTTL)
		}
	}
	return clientip.Contains(t.loaded, addr)
}

// PGTunerLoad reads the admin's list and the Plex server's address from the home's
// database. lookup resolves a host name (nil: the system resolver).
func PGTunerLoad(pool *pgxpool.Pool, lookup func(ctx context.Context, host string) ([]netip.Addr, error)) func(context.Context) ([]netip.Prefix, error) {
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	return func(ctx context.Context) ([]netip.Prefix, error) {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		var out []netip.Prefix
		var raw []byte
		err := pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, HDHRAllowedKey).Scan(&raw)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return nil, err
		default:
			out = append(out, ParseAllowed(raw)...)
		}
		var plexURL *string
		err = pool.QueryRow(ctx, `SELECT settings->>'url' FROM extensions WHERE name = 'plex' AND enabled`).Scan(&plexURL)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if plexURL != nil {
			out = append(out, hostPrefixes(ctx, *plexURL, lookup)...)
		}
		return out, nil
	}
}

// ParseAllowed reads the stored list; an entry that is not an address or a range, or
// that would allow every address, is dropped.
func ParseAllowed(raw []byte) []netip.Prefix {
	var doc struct {
		Addresses []string `json:"addresses"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	var out []netip.Prefix
	for _, a := range doc.Addresses {
		p, err := clientip.ParseEntry(strings.TrimSpace(a))
		if err != nil || !p.IsValid() || p.Bits() == 0 {
			continue
		}
		out = append(out, p)
	}
	return out
}

// hostPrefixes is the addresses of the host in rawURL (a Plex server's URL): the
// address itself, or what its name resolves to. Nothing when it cannot be read.
func hostPrefixes(ctx context.Context, rawURL string, lookup func(context.Context, string) ([]netip.Addr, error)) []netip.Prefix {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Hostname() == "" {
		return nil
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.WithZone("").Unmap()
		return []netip.Prefix{netip.PrefixFrom(ip, ip.BitLen())}
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil
	}
	var out []netip.Prefix
	for _, ip := range ips {
		ip = ip.WithZone("").Unmap()
		out = append(out, netip.PrefixFrom(ip, ip.BitLen()))
	}
	return out
}
