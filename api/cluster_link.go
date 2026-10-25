package main

// The home's end of the cluster link (Epic 35 story 35.16, design §18): its CA and the
// certificates it issues, its server certificate on the API's port (TLS sniffed on the
// first byte), the control channel and the data streams, the link's mode (prefer, then
// require by itself once every machine held the link for a day) and the front door's
// pulls from the machines that encode a channel.

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/ca"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/link"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/linkctx"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/rules"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// linkRules are rules.cluster's link settings.
type linkRules struct {
	Mode         string
	AutoRequire  bool
	MaxStreams   int
	GraceSeconds int
	CertDays     int
	WebHTTPS     bool
}

func defaultLinkRules() linkRules {
	return linkRules{Mode: linkctx.ModePrefer, AutoRequire: true, MaxStreams: 32, GraceSeconds: 60, CertDays: ca.LeafDays}
}

func linkRulesFrom(m map[string]any) linkRules {
	lr := defaultLinkRules()
	if s, ok := m["link_mode"].(string); ok && (s == linkctx.ModeOff || s == linkctx.ModePrefer || s == linkctx.ModeRequire) {
		lr.Mode = s
	}
	if b, ok := m["link_auto_require"].(bool); ok {
		lr.AutoRequire = b
	}
	num := func(key string, into *int) {
		switch v := m[key].(type) {
		case float64:
			*into = int(v)
		case int:
			*into = v
		case json.Number:
			if n, err := v.Int64(); err == nil {
				*into = int(n)
			}
		}
	}
	num("link_max_streams", &lr.MaxStreams)
	num("link_stream_grace_seconds", &lr.GraceSeconds)
	num("tls_cert_days", &lr.CertDays)
	if b, ok := m["tls_web_https"].(bool); ok {
		lr.WebHTTPS = b
	}
	return lr
}

// homeLink is the home's cluster link.
type homeLink struct {
	db       *sql.DB
	ca       *ca.CA
	srv      *link.Server
	nodes    cluster.Store
	notifier notify.Notifier
	logger   *slog.Logger

	mu    sync.Mutex
	rules linkRules
	down  map[string]time.Time // machines whose session ended, since when
	sans  []string
}

// startHomeLink loads (or makes) the CA and starts the link's server; nil when the CA is
// unavailable (the link is then off: every machine keeps the older paths).
func startHomeLink(ctx context.Context, db *sql.DB, store secrets.Store, nodes cluster.Store, name string,
	logger *slog.Logger) *homeLink {
	c, err := ca.LoadOrCreate(ctx, ca.Dir(os.Getenv), store, name)
	if err != nil {
		logger.Warn("link: the cluster CA is unavailable; the machines keep the older paths", "err", err,
			"event", "link_ca_failed")
		return nil
	}
	c.Store = ca.SQLStore{DB: db}
	hl := &homeLink{db: db, ca: c, nodes: nodes, logger: logger, rules: defaultLinkRules(), down: map[string]time.Time{}}
	hl.srv = &link.Server{CA: c, Certs: c.Store, Logger: logger, Settings: hl.settings, OnSession: hl.onSession,
		Alert: hl.alert,
		Approved: func(ctx context.Context, id string) (bool, error) {
			n, err := nodes.Node(ctx, id)
			if errors.Is(err, cluster.ErrNotFound) {
				return false, nil
			}
			return err == nil && n.State == cluster.StateApproved, err
		}}
	if u, err := url.Parse(os.Getenv("MAKTABA_STREAMING_HTTP_URL")); err == nil && u.Host != "" {
		// the VOD fallback (design §11.5) reads the home's own streaming port
		hl.srv.Targets = map[string]http.Handler{link.PurposeVOD: httputil.NewSingleHostReverseProxy(u)}
	}
	hl.refreshRules(ctx)
	hl.refreshServerCert(ctx)
	logger.Info("link: the cluster CA is ready", "ca_sha256", c.SHA256, "event", "link_ca_ready")
	return hl
}

// run keeps the rules, the server certificate and the mode current until ctx ends.
func (hl *homeLink) run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n++
		hl.refreshRules(ctx)
		hl.clearBroken(ctx)
		hl.autoRequire(ctx)
		if n%10 == 0 {
			hl.refreshServerCert(ctx) // every 5 minutes: the home's addresses may move (EC1)
		}
	}
}

func (hl *homeLink) refreshRules(ctx context.Context) {
	m, err := rules.Effective(ctx, hl.db, "cluster")
	if err != nil {
		return
	}
	lr := linkRulesFrom(m)
	hl.mu.Lock()
	hl.rules = lr
	hl.mu.Unlock()
	linkctx.SetMode(lr.Mode)
}

func (hl *homeLink) linkRules() linkRules {
	hl.mu.Lock()
	defer hl.mu.Unlock()
	return hl.rules
}

func (hl *homeLink) settings() link.Settings {
	lr := hl.linkRules()
	return link.Settings{Mode: lr.Mode, MaxStreams: lr.MaxStreams, StreamGrace: time.Duration(lr.GraceSeconds) * time.Second}
}

// homeSANs are the home's addresses (design §18.2): its host names, every address of
// its interfaces, and the addresses its machines reach it at (their node.json).
func (hl *homeLink) homeSANs(ctx context.Context) []string {
	set := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	if h, err := os.Hostname(); err == nil && h != "" {
		set[strings.ToLower(h)] = true
		set[strings.ToLower(strings.SplitN(h, ".", 2)[0])+".local"] = true
	}
	if h := cluster.StableHostName(); h != "" {
		set[strings.ToLower(h)] = true
		set[strings.ToLower(h)+".local"] = true
	}
	if ifaces, err := net.InterfaceAddrs(); err == nil {
		for _, a := range ifaces {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLinkLocalUnicast() && !ipn.IP.IsMulticast() {
				set[ipn.IP.String()] = true
			}
		}
	}
	for _, env := range []string{"MAKTABA_WEB_URL", "MAKTABA_PUBLIC_BASE_URL"} {
		if u, err := url.Parse(os.Getenv(env)); err == nil && u.Hostname() != "" {
			set[strings.ToLower(u.Hostname())] = true
		}
	}
	if nodes, err := hl.nodes.Nodes(ctx); err == nil {
		for _, n := range nodes {
			if n.APIURL != nil {
				if u, err := url.Parse(*n.APIURL); err == nil && u.Hostname() != "" {
					set[strings.ToLower(u.Hostname())] = true
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// refreshServerCert issues the home's server certificate again when its addresses moved.
func (hl *homeLink) refreshServerCert(ctx context.Context) {
	sans := hl.homeSANs(ctx)
	hl.mu.Lock()
	same := len(sans) == len(hl.sans)
	for i := range sans {
		if !same || sans[i] != hl.sans[i] {
			same = false
			break
		}
	}
	hl.mu.Unlock()
	if same {
		return
	}
	pair, is, err := hl.ca.IssueHome(ctx, ca.KindServer, sans, "", 365)
	if err != nil {
		hl.logger.Warn("link: the home's server certificate could not be issued", "err", err, "event", "link_server_cert_failed")
		return
	}
	hl.srv.SetServerCertificate(pair)
	hl.mu.Lock()
	hl.sans = sans
	hl.mu.Unlock()
	hl.logger.Info("link: the home's server certificate issued for its addresses", "serial", is.Serial,
		"addresses", len(sans), "event", "link_server_cert")
}

// IssueCert implements agent.CertIssuer (cert.csr on a machine's channel session).
func (hl *homeLink) IssueCert(ctx context.Context, nodeID, csrPEM string) (agent.CertIssue, error) {
	is, err := hl.ca.SignNode(ctx, []byte(csrPEM), nodeID, hl.linkRules().CertDays)
	if err != nil {
		return agent.CertIssue{}, err
	}
	return agent.CertIssue{Cert: string(is.PEM), CACert: string(hl.ca.CertPEM), CASHA256: hl.ca.SHA256, NotAfter: is.NotAfter}, nil
}

// JoinTLS signs a joining machine's CSR for its sealed credentials.
func (hl *homeLink) JoinTLS(ctx context.Context, nodeID, csrPEM string) (*cluster.PayloadTLS, error) {
	is, err := hl.ca.SignNode(ctx, []byte(csrPEM), nodeID, hl.linkRules().CertDays)
	if err != nil {
		return nil, err
	}
	return &cluster.PayloadTLS{CACert: string(hl.ca.CertPEM), CASHA256: hl.ca.SHA256, Cert: string(is.PEM)}, nil
}

// revoke closes a revoked (or removed) machine's link at once and refuses its
// certificates.
func (hl *homeLink) revoke(nodeID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := hl.ca.Store.RevokeNode(ctx, nodeID, time.Now().UTC()); err != nil {
		hl.logger.Warn("link: a revoked machine's certificates could not be marked", "node_id", nodeID, "err", err,
			"event", "link_revoke_failed")
	}
	hl.srv.Revoke(nodeID)
}

// onSession records since when a machine has held its link without a break
// (cluster_nodes.link_since): a drop shorter than the stream grace is no break.
func (hl *homeLink) onSession(nodeID string, up bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hl.mu.Lock()
	if up {
		delete(hl.down, nodeID)
	} else {
		hl.down[nodeID] = time.Now()
	}
	hl.mu.Unlock()
	if up {
		_, _ = hl.db.ExecContext(ctx, `UPDATE cluster_nodes SET link_since = COALESCE(link_since, now()) WHERE id = $1`, nodeID)
	}
}

// clearBroken ends link_since of the machines whose session stayed down past the grace.
func (hl *homeLink) clearBroken(ctx context.Context) {
	grace := time.Duration(hl.linkRules().GraceSeconds) * time.Second
	hl.mu.Lock()
	var gone []string
	for id, at := range hl.down {
		if time.Since(at) > grace {
			gone = append(gone, id)
			delete(hl.down, id)
		}
	}
	hl.mu.Unlock()
	for _, id := range gone {
		if hl.srv.Live(id) {
			continue
		}
		_, _ = hl.db.ExecContext(ctx, `UPDATE cluster_nodes SET link_since = NULL WHERE id = $1`, id)
	}
}

// requireAfter is how long every machine must have held its link before the home
// requires it.
const requireAfter = 24 * time.Hour

// autoRequire switches prefer to require once every approved machine has held its link
// for a day (design §18.7); an admin sets it back on the rules page.
func (hl *homeLink) autoRequire(ctx context.Context) {
	lr := hl.linkRules()
	if lr.Mode != linkctx.ModePrefer || !lr.AutoRequire {
		return
	}
	ready, total, err := hl.linkReady(ctx)
	if err != nil || total == 0 || ready < total {
		return
	}
	if err := setClusterRule(ctx, hl.db, "link_mode", linkctx.ModeRequire); err != nil {
		hl.logger.Warn("link: the switch to require failed", "err", err, "event", "link_require_failed")
		return
	}
	hl.refreshRules(ctx)
	hl.logger.Info("link: every machine held the link for a day: the link is now required", "machines", total,
		"event", "link_require_auto")
	if hl.notifier != nil {
		_ = hl.notifier.Notify(ctx, "cluster.link.required", notify.Payload{Severity: notify.SeverityInfo,
			TitleKey: "notify.cluster.link.required", Params: map[string]any{"machines": total}, Link: "/admin/machines",
			Subject: "cluster_link"})
	}
}

// linkReady counts the approved machines and those that held their link for a day.
func (hl *homeLink) linkReady(ctx context.Context) (ready, total int, err error) {
	err = hl.db.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE link_since IS NOT NULL AND link_since <= $1)
		FROM cluster_nodes WHERE state = 'approved'`, time.Now().Add(-requireAfter)).Scan(&total, &ready)
	return ready, total, err
}

// setClusterRule stores one rules.cluster setting (app_settings rules.cluster, the
// changes from the defaults).
func setClusterRule(ctx context.Context, db *sql.DB, key string, value any) error {
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'rules.cluster'`).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	m := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	m[key] = value
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO app_settings (key, value, updated_at) VALUES ('rules.cluster', $1::jsonb, now())
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, string(b))
	return err
}

func (hl *homeLink) alert(nodeID, detail string) {
	hl.logger.Warn("link: repeated stream ticket reuse", "node_id", nodeID, "detail", detail, "event", "link_ticket_reuse_alert")
	if hl.notifier == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = hl.notifier.Notify(ctx, "cluster.link.ticket_reuse", notify.Payload{Severity: notify.SeverityWarn,
		TitleKey: "notify.cluster.link.ticket_reuse", Params: map[string]any{"node_id": nodeID, "detail": detail},
		Link: "/admin/machines", Subject: "cluster_link_reuse:" + nodeID})
}

// LinkInfo is the link on the Machines page.
type LinkInfo struct {
	Mode        string `json:"mode"`
	AutoRequire bool   `json:"auto_require"`
	CASHA256    string `json:"ca_sha256"`
	CAURL       string `json:"ca_url"`
	WebHTTPS    bool   `json:"web_https"`
	// Ready / Machines: the approved machines that held their link a day, of all.
	Ready    int                 `json:"ready"`
	Machines int                 `json:"machines"`
	Nodes    map[string]LinkNode `json:"nodes"`
}

// LinkNode is one machine's link.
type LinkNode struct {
	Connected    bool       `json:"connected"`
	Since        *time.Time `json:"since,omitempty"`
	CertNotAfter *time.Time `json:"cert_not_after,omitempty"`
	HasCert      bool       `json:"has_cert"`
}

// info is the link's state for GET /api/cluster.
func (hl *homeLink) info(ctx context.Context) any {
	lr := hl.linkRules()
	out := LinkInfo{Mode: lr.Mode, AutoRequire: lr.AutoRequire, CASHA256: hl.ca.SHA256, CAURL: link.PathCACert,
		WebHTTPS: lr.WebHTTPS, Nodes: map[string]LinkNode{}}
	out.Ready, out.Machines, _ = hl.linkReady(ctx)
	rows, err := hl.db.QueryContext(ctx, `SELECT id::text, link_since, cert_not_after, cert_serial IS NOT NULL
		FROM cluster_nodes WHERE state = 'approved'`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id string
			var since, notAfter sql.NullTime
			var has bool
			if rows.Scan(&id, &since, &notAfter, &has) != nil {
				continue
			}
			n := LinkNode{Connected: hl.srv.Live(id), HasCert: has}
			if since.Valid {
				n.Since = &since.Time
			}
			if notAfter.Valid {
				n.CertNotAfter = &notAfter.Time
			}
			out.Nodes[id] = n
		}
	}
	return out
}

// mount adds the link's routes to the home's API.
func (hl *homeLink) mount(r chi.Router) {
	r.Get(link.PathLink, hl.srv.ServeLink)
	r.Get(link.PathStream, hl.srv.ServeStream)
	r.Get(link.PathCACert, hl.srv.ServeCACert)
	r.Get(link.PathPull, hl.servePull)
}

// servePull is GET /api/cluster/link/pull?node=&purpose=&path= on the home's loopback,
// with the supervisor's local token: the home's front door reading a channel a machine
// encodes (tv_pull) or a push relay's source (relay_pull) over that machine's link.
func (hl *homeLink) servePull(w http.ResponseWriter, r *http.Request) {
	if !localToken(r) {
		http.Error(w, "the local token is required", http.StatusUnauthorized)
		return
	}
	q := r.URL.Query()
	node, purpose, path := q.Get("node"), q.Get("purpose"), q.Get("path")
	if purpose == "" {
		purpose = link.PurposeTVPull
	}
	if node == "" || !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		http.Error(w, "node and path are required", http.StatusBadRequest)
		return
	}
	out, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://node"+path, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for k, vs := range r.Header {
		switch http.CanonicalHeaderKey(k) {
		case "Connection", "Upgrade", "Keep-Alive", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding",
			"X-Maktaba-Local-Token", "Host":
			continue
		}
		out.Header[k] = vs
	}
	resp, err := hl.srv.PullHTTP(r.Context(), node, purpose, out)
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, link.ErrNoLink) {
			code = http.StatusServiceUnavailable
		}
		http.Error(w, err.Error(), code)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		w.Header()[k] = vs
	}
	w.WriteHeader(resp.StatusCode)
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				hl.logger.Debug("link: a pull ended", "node_id", node, "err", err, "event", "link_pull_ended")
			}
			return
		}
	}
}

// localToken reports whether a request carries this machine's supervisor token (the
// processes on this machine; never sent across the network).
func localToken(r *http.Request) bool {
	got, file := r.Header.Get("X-Maktaba-Local-Token"), strings.TrimSpace(os.Getenv("MAKTABA_SUPERVISOR_TOKEN_FILE"))
	if got == "" || file == "" {
		return false
	}
	b, err := os.ReadFile(file) //nolint:gosec // this machine's own token file
	want := strings.TrimSpace(string(b))
	return err == nil && want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
