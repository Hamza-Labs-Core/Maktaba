package stores

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/direct"
)

// Direct is direct remote access's state (Epic 50 story 50.15): what each home reported of its
// direct listener, its certificate and the outside check, and the ACME challenges.
type Direct struct {
	DB *sql.DB
}

// NewDirect wraps db.
func NewDirect(db *sql.DB) *Direct { return &Direct{DB: db} }

// SetEndpoints records a home's report (META_ENDPOINTS): its public address as the relay sees
// the tunnel come from, its external port, and its state (on, off, cgnat, mapping_failed). A
// changed address needs nothing else: names are computed from it.
func (d *Direct) SetEndpoints(ctx context.Context, serverID string, public netip.Addr, port int, state string) error {
	var ip, p any
	if public.IsValid() && port > 0 && state == "on" {
		ip, p = public.Unmap().String(), port
	}
	_, err := d.DB.ExecContext(ctx,
		`UPDATE servers SET direct_ip = $2::inet, direct_port = $3, direct_state = $4 WHERE id = $1`,
		serverID, ip, p, state)
	if err != nil {
		return err
	}
	if state == direct.CheckCGNAT {
		return d.SetCheck(ctx, serverID, direct.CheckCGNAT, 0)
	}
	return nil
}

// MaxLAN is how many LAN addresses a home's report keeps.
const MaxLAN = 4

// SetLAN records the LAN addresses (ip:port) of a home's direct listener from its report, for
// the home list's LAN entries (story 50.15 §3). Only private addresses with a port are kept, at
// most MaxLAN; none clears them.
func (d *Direct) SetLAN(ctx context.Context, serverID string, lan []netip.AddrPort) error {
	keep := make([]string, 0, MaxLAN)
	for _, a := range lan {
		ip := a.Addr().Unmap()
		if len(keep) == MaxLAN || a.Port() == 0 || ip.Zone() != "" || !ip.IsPrivate() {
			continue
		}
		keep = append(keep, netip.AddrPortFrom(ip, a.Port()).String())
	}
	var v any
	if len(keep) > 0 {
		v = pq.Array(keep)
	}
	_, err := d.DB.ExecContext(ctx, `UPDATE servers SET direct_lan = $2 WHERE id = $1`, serverID, v)
	return err
}

// LANHome is a home's LAN addresses with its certificate's names: the home list's local entries.
type LANHome struct {
	HomeID, Domain string
	LAN            []netip.AddrPort
}

// LANFor gives the homes among ids that hold a certificate, report direct access on (or on
// behind carrier-grade NAT, or with no port mapped: the LAN still works) and listed LAN
// addresses.
func (d *Direct) LANFor(ctx context.Context, ids []string) (map[string]LANHome, error) {
	out := map[string]LANHome{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := d.DB.QueryContext(ctx, `
        SELECT s.id, c.home_id, c.domain, s.direct_lan
          FROM servers s
          JOIN direct_certs c ON c.server_id = s.id AND c.cert_pem IS NOT NULL
         WHERE s.id = ANY($1::uuid[]) AND s.direct_state IN ('on', 'cgnat', 'mapping_failed')
           AND cardinality(s.direct_lan) > 0`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var lan []string
		var h LANHome
		if err := rows.Scan(&id, &h.HomeID, &h.Domain, pq.Array(&lan)); err != nil {
			return nil, err
		}
		for _, s := range lan {
			if a, err := netip.ParseAddrPort(s); err == nil {
				h.LAN = append(h.LAN, a)
			}
		}
		out[id] = h
	}
	return out, rows.Err()
}

// DomainUse is each domain's new certificates in the last 7 days and its homes.
func (d *Direct) DomainUse(ctx context.Context, now time.Time) ([]direct.DomainUse, error) {
	rows, err := d.DB.QueryContext(ctx, `
        SELECT domain,
               count(*) FILTER (WHERE issued_at > $1)::int,
               count(*)::int
          FROM direct_certs GROUP BY domain`, now.Add(-7*24*time.Hour))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []direct.DomainUse
	for rows.Next() {
		var u direct.DomainUse
		if err := rows.Scan(&u.Domain, &u.IssuedWeek, &u.Homes); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Cert is a home's certificate row.
type Cert struct {
	ServerID, HomeID, Domain, CA string
	CSR                          []byte
	CertPEM                      string
	IssuedAt, NotAfter           sql.NullTime
	Error                        string
}

// Assign gives a home its domain and CA: a home's first ask has them chosen by pick (the most
// room this week); later asks get the same ones, so its names never change. overflow says the
// week's budgets were all used (an alert). The home makes its CSR for the name it is given.
func (d *Direct) Assign(ctx context.Context, serverID string, pick func([]direct.DomainUse) (string, string, bool), now time.Time) (Cert, bool, error) {
	c, err := d.Cert(ctx, serverID)
	if err == nil {
		return c, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Cert{}, false, err
	}
	use, err := d.DomainUse(ctx, now)
	if err != nil {
		return Cert{}, false, err
	}
	domain, ca, overflow := pick(use)
	if domain == "" {
		return Cert{}, false, errors.New("no direct-name domain is configured")
	}
	c = Cert{ServerID: serverID, HomeID: direct.HomeID(serverID), Domain: domain, CA: ca}
	_, err = d.DB.ExecContext(ctx, `
        INSERT INTO direct_certs (server_id, home_id, domain, ca) VALUES ($1, $2, $3, $4)
        ON CONFLICT (server_id) DO NOTHING`, serverID, c.HomeID, domain, ca)
	if err != nil {
		return Cert{}, false, err
	}
	// a concurrent ask may have won: the row is the answer
	c, err = d.Cert(ctx, serverID)
	return c, overflow, err
}

// PutCSR queues a home's CSR for the worker, assigning its domain first if it has none (Assign).
func (d *Direct) PutCSR(ctx context.Context, serverID string, csr []byte, pick func([]direct.DomainUse) (string, string, bool), now time.Time) (Cert, bool, error) {
	c, overflow, err := d.Assign(ctx, serverID, pick, now)
	if err != nil {
		return Cert{}, false, err
	}
	_, err = d.DB.ExecContext(ctx, `UPDATE direct_certs SET csr = $2, error = NULL, updated_at = now() WHERE server_id = $1`, serverID, csr)
	c.CSR = csr
	return c, overflow, err
}

// Cert reads a home's certificate row (sql.ErrNoRows: none yet).
func (d *Direct) Cert(ctx context.Context, serverID string) (Cert, error) {
	var c Cert
	var pem, errText sql.NullString
	err := d.DB.QueryRowContext(ctx, `
        SELECT server_id, home_id, domain, ca, csr, cert_pem, issued_at, not_after, error
          FROM direct_certs WHERE server_id = $1`, serverID).
		Scan(&c.ServerID, &c.HomeID, &c.Domain, &c.CA, &c.CSR, &pem, &c.IssuedAt, &c.NotAfter, &errText)
	c.CertPEM, c.Error = pem.String, errText.String
	return c, err
}

// PendingCSRs are the CSRs waiting for the worker, oldest first.
func (d *Direct) PendingCSRs(ctx context.Context, limit int) ([]Cert, error) {
	rows, err := d.DB.QueryContext(ctx, `
        SELECT server_id FROM direct_certs WHERE csr IS NOT NULL ORDER BY updated_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	out := make([]Cert, 0, len(ids))
	for _, id := range ids {
		c, err := d.Cert(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// SetIssued keeps a chain: the first counts against the domain's week (issued_at), a renewal
// doesn't (renewed_at). ca is the CA it came from (an overflow CA is kept for renewals).
func (d *Direct) SetIssued(ctx context.Context, serverID, ca, chainPEM string, notAfter time.Time) error {
	_, err := d.DB.ExecContext(ctx, `
        UPDATE direct_certs SET cert_pem = $2, ca = $3, not_after = $4, csr = NULL, error = NULL,
               issued_at = COALESCE(issued_at, now()),
               renewed_at = CASE WHEN issued_at IS NULL THEN NULL ELSE now() END,
               delivered_at = NULL, updated_at = now()
         WHERE server_id = $1`, serverID, chainPEM, ca, notAfter)
	return err
}

// SetIssueError keeps why issuing failed; the CSR stays for the next try.
func (d *Direct) SetIssueError(ctx context.Context, serverID, why string) error {
	_, err := d.DB.ExecContext(ctx, `UPDATE direct_certs SET error = $2, updated_at = now() WHERE server_id = $1`, serverID, why)
	return err
}

// CertToDeliver is a chain the home hasn't been given yet ("" none).
func (d *Direct) CertToDeliver(ctx context.Context, serverID string) (chain, domain string, err error) {
	err = d.DB.QueryRowContext(ctx, `
        UPDATE direct_certs SET delivered_at = now()
         WHERE server_id = $1 AND cert_pem IS NOT NULL AND delivered_at IS NULL
        RETURNING cert_pem, domain`, serverID).Scan(&chain, &domain)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return chain, domain, err
}

// Due is a home whose direct access is checked from outside.
type Due struct {
	ServerID, HomeID, Domain string
	IP                       netip.Addr
	Port                     int
}

// DueChecks are homes reporting direct access on, with a certificate, not checked for every (or
// asked to be checked now).
func (d *Direct) DueChecks(ctx context.Context, every time.Duration, limit int) ([]Due, error) {
	rows, err := d.DB.QueryContext(ctx, `
        SELECT s.id, c.home_id, c.domain, host(s.direct_ip), s.direct_port
          FROM servers s
          JOIN direct_certs c ON c.server_id = s.id AND c.cert_pem IS NOT NULL
          LEFT JOIN direct_checks k ON k.server_id = s.id
         WHERE s.direct_state = 'on' AND s.direct_ip IS NOT NULL AND s.direct_port IS NOT NULL
           AND (k.checked_at IS NULL OR k.checked_at < now() - $1::interval
                OR k.requested_at > k.checked_at OR k.port IS DISTINCT FROM s.direct_port)
         ORDER BY k.checked_at NULLS FIRST LIMIT $2`, every.String(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Due
	for rows.Next() {
		var u Due
		var ip string
		if err := rows.Scan(&u.ServerID, &u.HomeID, &u.Domain, &ip, &u.Port); err != nil {
			return nil, err
		}
		u.IP, _ = netip.ParseAddr(ip)
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetCheck keeps a check's result; the home is told of a changed one.
func (d *Direct) SetCheck(ctx context.Context, serverID, result string, port int) error {
	_, err := d.DB.ExecContext(ctx, `
        INSERT INTO direct_checks (server_id, result, port, checked_at) VALUES ($1, $2, $3, now())
        ON CONFLICT (server_id) DO UPDATE SET
               delivered_at = CASE WHEN direct_checks.result IS DISTINCT FROM EXCLUDED.result THEN NULL ELSE direct_checks.delivered_at END,
               result = EXCLUDED.result, port = EXCLUDED.port, checked_at = now()`, serverID, result, port)
	return err
}

// RequestCheck is the admin's Check now.
func (d *Direct) RequestCheck(ctx context.Context, serverID string) error {
	_, err := d.DB.ExecContext(ctx, `
        INSERT INTO direct_checks (server_id, requested_at) VALUES ($1, now())
        ON CONFLICT (server_id) DO UPDATE SET requested_at = now(), delivered_at = NULL`, serverID)
	return err
}

// CheckToDeliver is a result the home hasn't been told yet ("" none).
func (d *Direct) CheckToDeliver(ctx context.Context, serverID string) (result string, port int, at time.Time, err error) {
	err = d.DB.QueryRowContext(ctx, `
        UPDATE direct_checks SET delivered_at = now()
         WHERE server_id = $1 AND result IS NOT NULL AND delivered_at IS NULL
        RETURNING result, COALESCE(port, 0), COALESCE(checked_at, now())`, serverID).Scan(&result, &port, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, time.Time{}, nil
	}
	return result, port, at, err
}

// Reachable is a home's direct name when its last check passed (nil when not).
type Reachable struct {
	HomeID, Domain string
	IP             netip.Addr
	Port           int
}

// ReachableFor gives the homes among ids whose last check passed: the home list's direct entries.
func (d *Direct) ReachableFor(ctx context.Context, ids []string) (map[string]Reachable, error) {
	out := map[string]Reachable{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := d.DB.QueryContext(ctx, `
        SELECT s.id, c.home_id, c.domain, host(s.direct_ip), s.direct_port
          FROM servers s
          JOIN direct_certs c ON c.server_id = s.id AND c.cert_pem IS NOT NULL
          JOIN direct_checks k ON k.server_id = s.id AND k.result = 'ok' AND k.port = s.direct_port
         WHERE s.id = ANY($1::uuid[]) AND s.direct_state = 'on'`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, ip string
		var r Reachable
		if err := rows.Scan(&id, &r.HomeID, &r.Domain, &ip, &r.Port); err != nil {
			return nil, err
		}
		r.IP, _ = netip.ParseAddr(ip)
		out[id] = r
	}
	return out, rows.Err()
}

// ChallengeTXT serves the responder: the live values for name.
func (d *Direct) ChallengeTXT(ctx context.Context, name string) ([]string, error) {
	rows, err := d.DB.QueryContext(ctx, `SELECT value FROM acme_challenges WHERE name = $1 AND expires_at > now()`, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PutChallenge and DeleteChallenge are the issuer's (direct.ChallengeStore).
func (d *Direct) PutChallenge(ctx context.Context, name, value string, ttl time.Duration) error {
	_, err := d.DB.ExecContext(ctx, `
        INSERT INTO acme_challenges (name, value, expires_at) VALUES ($1, $2, now() + $3::interval)
        ON CONFLICT (name, value) DO UPDATE SET expires_at = EXCLUDED.expires_at`, name, value, ttl.String())
	return err
}

func (d *Direct) DeleteChallenge(ctx context.Context, name, value string) error {
	_, err := d.DB.ExecContext(ctx, `DELETE FROM acme_challenges WHERE (name = $1 AND value = $2) OR expires_at < now()`, name, value)
	return err
}
