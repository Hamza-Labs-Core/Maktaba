// Command relay-dns keeps relay.maktaba.tv and *.relay.maktaba.tv a DNS round robin over the
// relay pool's healthy relays (Epic 50 story 50.16 §4; internal/relaydns). It runs on the
// witness W: every 15 s it reads the relays table, probes each relay's /readyz on its public IP,
// and updates the A records through the Cloudflare API (TTL 60 s). It never removes the last
// record.
//
// Env:
//
//	RELAY_DNS_DB_URL     the cloud database (a role that can read the relays table)
//	CF_API_TOKEN_FILE    the Cloudflare token (Zone → DNS → Edit on the zone only), 0600
//	CF_ZONE_ID           the zone
//	CF_API               default https://api.cloudflare.com/client/v4
//	RELAY_DOMAIN         default relay.maktaba.tv (both it and *.<it> are kept)
//	RELAY_DNS_EVERY      default 15s
//	RELAY_DNS_PROBE_PORT default 443
//	RELAY_DNS_ONCE       1: one round, then exit (0 when it succeeded)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relaydns"
)

// settings are the command's env, read and checked.
type settings struct {
	dbURL, zone, api, token, domain string
	every                           time.Duration
	port                            int
	once                            bool
}

func load(getenv func(string) string, readFile func(string) ([]byte, error)) (settings, error) {
	get := func(k, def string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return def
	}
	s := settings{dbURL: get("RELAY_DNS_DB_URL", ""), zone: get("CF_ZONE_ID", ""), api: get("CF_API", ""),
		domain: get("RELAY_DOMAIN", "relay.maktaba.tv"), once: get("RELAY_DNS_ONCE", "") == "1"}
	if s.dbURL == "" || s.zone == "" {
		return s, errors.New("relay-dns: RELAY_DNS_DB_URL and CF_ZONE_ID are required")
	}
	path := get("CF_API_TOKEN_FILE", "")
	if path == "" {
		return s, errors.New("relay-dns: CF_API_TOKEN_FILE is required (the token is never read from the environment)")
	}
	b, err := readFile(path)
	if err != nil {
		return s, fmt.Errorf("relay-dns: reading the token file: %w", err)
	}
	if s.token = strings.TrimSpace(string(b)); s.token == "" {
		return s, errors.New("relay-dns: the token file is empty")
	}
	if s.every, err = time.ParseDuration(get("RELAY_DNS_EVERY", "15s")); err != nil || s.every <= 0 {
		return s, fmt.Errorf("relay-dns: RELAY_DNS_EVERY %q", get("RELAY_DNS_EVERY", ""))
	}
	if s.port, err = strconv.Atoi(get("RELAY_DNS_PROBE_PORT", "443")); err != nil || s.port <= 0 || s.port > 65535 {
		return s, fmt.Errorf("relay-dns: RELAY_DNS_PROBE_PORT %q", get("RELAY_DNS_PROBE_PORT", ""))
	}
	return s, nil
}

func (s settings) controller(store relay.PoolStore, logger *slog.Logger) *relaydns.Controller {
	return &relaydns.Controller{
		Relays: store,
		DNS:    &relaydns.Cloudflare{BaseURL: s.api, ZoneID: s.zone, Token: s.token},
		Names:  []string{s.domain, "*." + s.domain},
		TTL:    relaydns.TTL,
		Probe:  relaydns.ProbeReadyz(s.domain, s.port, 5*time.Second),
		Logger: logger,
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	s, err := load(os.Getenv, os.ReadFile)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(2)
	}
	pool, err := db.Open(db.WithConnectTimeout(s.dbURL, 5), 2, 1, 30*time.Minute)
	if err != nil {
		logger.Error("relay-dns: opening the database", "err", err)
		os.Exit(1)
	}
	defer func() { _ = pool.Close() }()
	c := s.controller(relay.PGPoolStore{DB: pool.DB}, logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if s.once {
		res, err := c.Reconcile(ctx)
		logger.Info("relay-dns: one round", "want", res.Want, "added", res.Added, "removed", res.Removed, "kept", res.Kept)
		if err != nil {
			logger.Error("relay-dns: the round failed", "err", err)
			os.Exit(1)
		}
		return
	}
	logger.Info("relay-dns: keeping the round robin", "domain", s.domain, "every", s.every.String())
	c.Run(ctx, s.every)
}
