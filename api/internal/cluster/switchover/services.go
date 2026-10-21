package switchover

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// A service of the home's network the pipeline talks to (Plex, dizqueTV): its extension,
// where its address may come from on this machine, how it is asked whether it answers.
type localService struct {
	ext    string // the extensions row
	urlEnv string // its address from this machine's environment, when the row has none
	probe  string // a path that answers 200 when the service is there
	// token is the service's token (Plex): its secret, else the environment's
	tokenSecret, tokenEnv string
}

var (
	plexService     = localService{ext: "plex", urlEnv: "MAKTABA_PLEX_URL", probe: "/identity", tokenSecret: "ext.plex.token", tokenEnv: "MAKTABA_PLEX_TOKEN"}
	dizquetvService = localService{ext: "dizquetv", urlEnv: "MAKTABA_DIZQUETV_URL", probe: "/api/channels"}
)

// serviceClient asks the services (their own network: never through a proxy).
var serviceClient = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}

// checkService checks, on the new home, that a service the old home used answers from
// here at the address the pipeline has for it (and with its token): moved. An address
// that was the old home's own (its loopback: the service runs there) is moved to the old
// home's network address (oldHost) when the service answers there: the pipeline's
// setting is changed to it. Otherwise a person sets where it is (needs you).
func checkService(ctx context.Context, db *sql.DB, store Secrets, s localService, oldHost string) Integration {
	var enabled bool
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT enabled, settings FROM extensions WHERE name = $1`, s.ext).Scan(&enabled, &raw)
	switch {
	case errors.Is(err, sql.ErrNoRows) || (err == nil && !enabled):
		return Integration{State: NotUsed}
	case err != nil:
		return Integration{State: Failed, Detail: err.Error()}
	}
	var settings map[string]any
	_ = json.Unmarshal(raw, &settings)
	addr, _ := settings["url"].(string)
	if strings.TrimSpace(addr) == "" {
		addr = os.Getenv(s.urlEnv)
	}
	addr = strings.TrimRight(strings.TrimSpace(addr), "/")
	if addr == "" {
		// the old home had it from its own environment: this machine does not
		return Integration{State: NeedsYou, Detail: "no_address"}
	}
	token := ""
	if s.tokenSecret != "" {
		if store != nil {
			if b, err := store.Get(ctx, s.tokenSecret); err == nil {
				token = string(b)
			}
		}
		if token == "" {
			token = os.Getenv(s.tokenEnv)
		}
		if token == "" {
			return Integration{State: Failed, Detail: "its token does not open with the key handed over"}
		}
	}
	err = probeService(ctx, addr+s.probe, token)
	if err == nil {
		return Integration{State: Moved, Detail: addr}
	}
	u, perr := url.Parse(addr)
	if perr != nil || !loopbackHost(u.Hostname()) {
		return Integration{State: Failed, Detail: err.Error()}
	}
	// the old home's own address: the service runs on the old home, reached from here at
	// its network address
	if oldHost != "" {
		moved := *u
		moved.Host = oldHost
		if p := u.Port(); p != "" {
			moved.Host = net.JoinHostPort(oldHost, p)
		}
		there := strings.TrimRight(moved.String(), "/")
		if there != addr && probeService(ctx, there+s.probe, token) == nil {
			if err := setServiceURL(ctx, db, s.ext, there); err != nil {
				return Integration{State: Failed, Detail: err.Error()}
			}
			return Integration{State: Moved, Detail: there}
		}
	}
	return Integration{State: NeedsYou, Detail: addr}
}

func loopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func probeService(ctx context.Context, u, token string) error {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
	resp, err := serviceClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %d", strings.SplitN(u, "?", 2)[0], resp.StatusCode)
	}
	return nil
}

// setServiceURL points the pipeline's extension at address (its stages read the setting
// anew at once).
func setServiceURL(ctx context.Context, db *sql.DB, ext, address string) error {
	if _, err := db.ExecContext(ctx, `UPDATE extensions SET settings = jsonb_set(COALESCE(settings, '{}'::jsonb), '{url}',
	    to_jsonb($2::text)), updated_at = now() WHERE name = $1`, ext, address); err != nil {
		return err
	}
	_, _ = db.ExecContext(ctx, `SELECT pg_notify('jobs.control', '{"table": "extensions"}')`)
	return nil
}

// oldHomeHost is the network host of the machine the home was before the latest switch
// (the old home, a machine of this one now): "" when not known.
func oldHomeHost(ctx context.Context, db *sql.DB) string {
	var web sql.NullString
	_ = db.QueryRowContext(ctx, `SELECT web_url FROM cluster_nodes WHERE approved_by = 'switchover'
	    ORDER BY approved_at DESC LIMIT 1`).Scan(&web)
	u, err := url.Parse(web.String)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// WebhookSeen is when the Cloudflare Notifications webhook last brought an event here
// (tvremote's handler keeps it).
const WebhookSeen = "tv_remote.webhook_seen"

// cfWebhook: Cloudflare sends its Live input notifications to the address set in its
// dashboard: moved once one reached this home since the switch (Cloudflare's own test
// notification will do); until then the address to set there (needs you).
func cfWebhook(ctx context.Context, db *sql.DB, webURL string) Integration {
	var has bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM home_secrets WHERE name = 'cf_webhook_secret')`).
		Scan(&has); err != nil || !has {
		return Integration{State: NotUsed}
	}
	var seen, since sql.NullTime
	_ = db.QueryRowContext(ctx, `SELECT (value #>> '{}')::timestamptz FROM app_settings WHERE key = $1`, WebhookSeen).Scan(&seen)
	_ = db.QueryRowContext(ctx, `SELECT started_at FROM cluster_terms ORDER BY term DESC LIMIT 1`).Scan(&since)
	if seen.Valid && (!since.Valid || seen.Time.After(since.Time)) {
		return Integration{State: Moved}
	}
	return Integration{State: NeedsYou, Detail: strings.TrimRight(webURL, "/") + "/api/tv/remote/webhook"}
}
