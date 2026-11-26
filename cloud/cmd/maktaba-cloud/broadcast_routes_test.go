package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/broadcastgate"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// routeKey uniquely identifies one registered route on a listener.
type routeKey struct {
	listener string
	method   string
	pattern  string
}

// apiRouteClassification explicitly classifies every route mounted on the api role's user and
// console listeners as either belonging to broadcasting/moderation (true, must be behind
// broadcastgate) or not (false, must not be under broadcastgate). Any new route fails this test
// until explicitly classified here rather than relying on handler-name heuristics.
var apiRouteClassification = map[routeKey]bool{
	// console listener
	{"console", "GET", "/healthz"}:                                           false,
	{"console", "GET", "/readyz"}:                                            false,
	{"console", "GET", "/v1/admin/abuse"}:                                    false,
	{"console", "POST", "/v1/admin/abuse/block"}:                             false,
	{"console", "POST", "/v1/admin/abuse/unblock"}:                           false,
	{"console", "GET", "/v1/admin/accounts"}:                                 false,
	{"console", "GET", "/v1/admin/accounts/{id}"}:                            false,
	{"console", "POST", "/v1/admin/accounts/{id}/plan"}:                      false,
	{"console", "POST", "/v1/admin/accounts/{id}/suspend"}:                   false,
	{"console", "POST", "/v1/admin/accounts/{id}/unsuspend"}:                 false,
	{"console", "GET", "/v1/admin/audit"}:                                    false,
	{"console", "GET", "/v1/admin/broadcast/analytics"}:                      true,
	{"console", "GET", "/v1/admin/broadcast/geo-deny"}:                       true,
	{"console", "PUT", "/v1/admin/broadcast/geo-deny"}:                       true,
	{"console", "POST", "/v1/admin/broadcast/invites/{id}/revoke"}:           true,
	{"console", "GET", "/v1/admin/broadcast/revocations"}:                    true,
	{"console", "POST", "/v1/admin/broadcast/revocations"}:                   true,
	{"console", "POST", "/v1/admin/broadcast/revocations/{id}/lift"}:         true,
	{"console", "GET", "/v1/admin/errors"}:                                   false,
	{"console", "GET", "/v1/admin/errors/cloud/{fp}"}:                        false,
	{"console", "GET", "/v1/admin/fleet"}:                                    false,
	{"console", "GET", "/v1/admin/licenses"}:                                 false,
	{"console", "POST", "/v1/admin/licenses/{id}/revoke"}:                    false,
	{"console", "POST", "/v1/admin/licenses/{id}/unrevoke"}:                  false,
	{"console", "GET", "/v1/admin/logs/around"}:                              false,
	{"console", "GET", "/v1/admin/logs/search"}:                              false,
	{"console", "GET", "/v1/admin/metrics/bandwidth"}:                        false,
	{"console", "GET", "/v1/admin/metrics/export"}:                           false,
	{"console", "GET", "/v1/admin/metrics/geo"}:                              false,
	{"console", "GET", "/v1/admin/metrics/overview"}:                         false,
	{"console", "GET", "/v1/admin/metrics/push"}:                             false,
	{"console", "GET", "/v1/admin/metrics/subscriptions"}:                    false,
	{"console", "GET", "/v1/admin/moderation"}:                               true,
	{"console", "POST", "/v1/admin/moderation/actions"}:                      true,
	{"console", "POST", "/v1/admin/moderation/actions/{id}/restore"}:         true,
	{"console", "POST", "/v1/admin/moderation/counter-notices/{id}/lawsuit"}: true,
	{"console", "GET", "/v1/admin/moderation/export"}:                        true,
	{"console", "POST", "/v1/admin/moderation/notices"}:                      true,
	{"console", "POST", "/v1/admin/moderation/notices/{id}/reject"}:          true,
	{"console", "POST", "/v1/admin/moderation/notices/{id}/withdraw"}:        true,
	{"console", "POST", "/v1/admin/moderation/notifiers/suspend"}:            true,
	{"console", "POST", "/v1/admin/moderation/publishers/{id}/reinstate"}:    true,
	{"console", "POST", "/v1/admin/moderation/reports/{id}/dismiss"}:         true,
	{"console", "POST", "/v1/admin/moderation/strikes/{id}/withdraw"}:        true,
	{"console", "GET", "/v1/admin/privacy/processing-records"}:               false,
	{"console", "DELETE", "/v1/admin/privacy/users/{id}"}:                    false,
	{"console", "GET", "/v1/admin/reports"}:                                  false,
	{"console", "GET", "/v1/admin/reports/{id}"}:                             false,
	{"console", "POST", "/v1/admin/reports/{id}/issue"}:                      false,
	{"console", "POST", "/v1/admin/reports/{id}/reply"}:                      false,
	{"console", "GET", "/v1/admin/reports/{id}/screenshot"}:                  false,
	{"console", "POST", "/v1/admin/reports/{id}/status"}:                     false,
	{"console", "GET", "/v1/admin/revenue"}:                                  false,
	{"console", "GET", "/v1/admin/servers"}:                                  false,
	{"console", "POST", "/v1/admin/servers/{id}/broadcast"}:                  true,
	{"console", "GET", "/v1/admin/servers/{id}/broadcast/invites"}:           true,
	{"console", "GET", "/v1/admin/symbols"}:                                  false,
	{"console", "GET", "/v1/admin/usage"}:                                    false,
	{"console", "GET", "/v1/staff/config"}:                                   false,
	{"console", "POST", "/v1/staff/enroll/finish"}:                           false,
	{"console", "POST", "/v1/staff/enroll/passkey/begin"}:                    false,
	{"console", "POST", "/v1/staff/enroll/passkey/finish"}:                   false,
	{"console", "POST", "/v1/staff/enroll/start"}:                            false,
	{"console", "POST", "/v1/staff/login"}:                                   false,
	{"console", "POST", "/v1/staff/logout"}:                                  false,
	{"console", "GET", "/v1/staff/me"}:                                       false,
	{"console", "POST", "/v1/staff/mfa/passkey/begin"}:                       false,
	{"console", "POST", "/v1/staff/mfa/passkey/finish"}:                      false,
	{"console", "POST", "/v1/staff/mfa/recovery"}:                            false,
	{"console", "POST", "/v1/staff/mfa/totp"}:                                false,
	{"console", "POST", "/v1/staff/reauth/passkey/begin"}:                    false,
	{"console", "POST", "/v1/staff/reauth/passkey/finish"}:                   false,
	{"console", "POST", "/v1/staff/reauth/totp"}:                             false,
	{"console", "POST", "/v1/staff/refresh"}:                                 false,
	{"console", "GET", "/v1/staff/sessions"}:                                 false,
	{"console", "POST", "/v1/staff/sessions/revoke-all"}:                     false,

	// user listener
	{"user", "GET", "/.well-known/maktaba-cloud-version"}:                          false,
	{"user", "GET", "/.well-known/maktaba-entitlement-key"}:                        false,
	{"user", "GET", "/_edge/publishers/{id}"}:                                      true,
	{"user", "POST", "/_edge/usage"}:                                               true,
	{"user", "GET", "/healthz"}:                                                    false,
	{"user", "GET", "/metrics"}:                                                    false,
	{"user", "GET", "/readyz"}:                                                     false,
	{"user", "POST", "/v1/account/deletion/cancel"}:                                false,
	{"user", "GET", "/v1/account/export"}:                                          false,
	{"user", "DELETE", "/v1/account/me"}:                                           false,
	{"user", "GET", "/v1/account/me"}:                                              false,
	{"user", "PATCH", "/v1/account/me"}:                                            false,
	{"user", "POST", "/v1/account/password"}:                                       false,
	{"user", "GET", "/v1/account/sessions"}:                                        false,
	{"user", "POST", "/v1/account/sessions/revoke-all"}:                            false,
	{"user", "DELETE", "/v1/account/sessions/{id}"}:                                false,
	{"user", "POST", "/v1/auth/login"}:                                             false,
	{"user", "POST", "/v1/auth/logout"}:                                            false,
	{"user", "GET", "/v1/auth/oauth/apple"}:                                        false,
	{"user", "POST", "/v1/auth/oauth/apple/callback"}:                              false,
	{"user", "GET", "/v1/auth/oauth/google"}:                                       false,
	{"user", "GET", "/v1/auth/oauth/google/callback"}:                              false,
	{"user", "POST", "/v1/auth/password/forgot"}:                                   false,
	{"user", "POST", "/v1/auth/password/reset"}:                                    false,
	{"user", "GET", "/v1/auth/providers"}:                                          false,
	{"user", "POST", "/v1/auth/refresh"}:                                           false,
	{"user", "POST", "/v1/auth/register"}:                                          false,
	{"user", "POST", "/v1/auth/verify-email"}:                                      false,
	{"user", "POST", "/v1/auth/verify-email/resend"}:                               false,
	{"user", "GET", "/v1/billing/broadcast"}:                                       true,
	{"user", "PUT", "/v1/billing/broadcast"}:                                       true,
	{"user", "PATCH", "/v1/billing/broadcast/extras"}:                              true,
	{"user", "PATCH", "/v1/billing/broadcast/payg"}:                                true,
	{"user", "POST", "/v1/billing/checkout"}:                                       false,
	{"user", "PATCH", "/v1/billing/extra-streams"}:                                 false,
	{"user", "GET", "/v1/billing/me"}:                                              false,
	{"user", "GET", "/v1/billing/payg"}:                                            false,
	{"user", "PATCH", "/v1/billing/payg"}:                                          false,
	{"user", "GET", "/v1/billing/plans"}:                                           false,
	{"user", "POST", "/v1/billing/portal"}:                                         false,
	{"user", "GET", "/v1/billing/usage"}:                                           false,
	{"user", "POST", "/v1/billing/webhook/{provider}"}:                             false,
	{"user", "GET", "/v1/broadcast/access"}:                                        true,
	{"user", "GET", "/v1/broadcast/agreement"}:                                     true,
	{"user", "POST", "/v1/broadcast/agreement"}:                                    true,
	{"user", "GET", "/v1/broadcast/analytics"}:                                     true,
	{"user", "POST", "/v1/broadcast/channels"}:                                     true,
	{"user", "DELETE", "/v1/broadcast/channels/{id}"}:                              true,
	{"user", "PUT", "/v1/broadcast/channels/{id}/access"}:                          true,
	{"user", "GET", "/v1/broadcast/home/analytics"}:                                true,
	{"user", "GET", "/v1/broadcast/homes/{server}/access"}:                         true,
	{"user", "PUT", "/v1/broadcast/homes/{server}/channels/{id}/access"}:           true,
	{"user", "GET", "/v1/broadcast/homes/{server}/invites"}:                        true,
	{"user", "POST", "/v1/broadcast/homes/{server}/invites"}:                       true,
	{"user", "DELETE", "/v1/broadcast/homes/{server}/invites/{id}"}:                true,
	{"user", "GET", "/v1/broadcast/homes/{server}/invites/{id}/devices"}:           true,
	{"user", "DELETE", "/v1/broadcast/homes/{server}/invites/{id}/devices/{pass}"}: true,
	{"user", "PUT", "/v1/broadcast/homes/{server}/viewer-limits"}:                  true,
	{"user", "POST", "/v1/broadcast/ingest-token"}:                                 true,
	{"user", "GET", "/v1/broadcast/invites"}:                                       true,
	{"user", "POST", "/v1/broadcast/invites"}:                                      true,
	{"user", "DELETE", "/v1/broadcast/invites/{id}"}:                               true,
	{"user", "GET", "/v1/broadcast/invites/{id}/devices"}:                          true,
	{"user", "DELETE", "/v1/broadcast/invites/{id}/devices/{pass}"}:                true,
	{"user", "GET", "/v1/broadcast/moderation"}:                                    true,
	{"user", "POST", "/v1/broadcast/moderation/counter-notices"}:                   true,
	{"user", "GET", "/v1/broadcast/revocations"}:                                   true,
	{"user", "GET", "/v1/broadcast/state"}:                                         true,
	{"user", "GET", "/v1/broadcast/takedowns"}:                                     true,
	{"user", "POST", "/v1/broadcast/takedowns/{id}/ack"}:                           true,
	{"user", "PUT", "/v1/broadcast/viewer-limits"}:                                 true,
	{"user", "GET", "/v1/dashboard/servers"}:                                       false,
	{"user", "GET", "/v1/homes"}:                                                   false,
	{"user", "GET", "/v1/homes/assertion-key"}:                                     false,
	{"user", "POST", "/v1/homes/{id}/assertion"}:                                   false,
	{"user", "GET", "/v1/homes/{id}/link"}:                                         false,
	{"user", "DELETE", "/v1/homes/{id}/reports"}:                                   false,
	{"user", "POST", "/v1/homes/{id}/reports"}:                                     false,
	{"user", "GET", "/v1/homes/{id}/reports/replies"}:                              false,
	{"user", "GET", "/v1/homes/{id}/tv-codes/{code}"}:                              false,
	{"user", "POST", "/v1/homes/{id}/tv-codes/{code}/claim"}:                       false,
	{"user", "GET", "/v1/licenses/revocations"}:                                    false,
	{"user", "POST", "/v1/mail/webhook/resend"}:                                    false,
	{"user", "POST", "/v1/moderation/notices"}:                                     true,
	{"user", "POST", "/v1/moderation/reports"}:                                     true,
	{"user", "DELETE", "/v1/push/devices"}:                                         false,
	{"user", "POST", "/v1/push/devices"}:                                           false,
	{"user", "POST", "/v1/push/dispatch"}:                                          false,
	{"user", "GET", "/v1/servers"}:                                                 false,
	{"user", "POST", "/v1/servers/claims"}:                                         false,
	{"user", "POST", "/v1/servers/claims/redeem"}:                                  false,
	{"user", "GET", "/v1/servers/{id}"}:                                            false,
	{"user", "POST", "/v1/servers/{id}/heartbeat"}:                                 false,
	{"user", "GET", "/v1/servers/{id}/license"}:                                    false,
	{"user", "PUT", "/v1/servers/{id}/links"}:                                      false,
	{"user", "POST", "/v1/symbols"}:                                                false,
	{"user", "POST", "/v1/telemetry"}:                                              false,
	{"user", "POST", "/v1/tv/codes"}:                                               false,
	{"user", "POST", "/v1/tv/codes/poll"}:                                          false,
	{"user", "GET", "/v1/tv/codes/{code}"}:                                         false,
	{"user", "POST", "/v1/tv/codes/{code}/approve"}:                                false,
}

func walkAPIListeners(t *testing.T, user, console http.Handler) []routeKey {
	t.Helper()
	var out []routeKey
	for _, pair := range []struct {
		name string
		h    http.Handler
	}{
		{"user", user},
		{"console", console},
	} {
		mux, ok := pair.h.(chi.Routes)
		if !ok {
			t.Fatalf("the %s listener is a %T", pair.name, pair.h)
		}
		if err := chi.Walk(mux, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			out = append(out, routeKey{listener: pair.name, method: method, pattern: pattern})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].listener != out[j].listener {
			return out[i].listener < out[j].listener
		}
		if out[i].pattern != out[j].pattern {
			return out[i].pattern < out[j].pattern
		}
		return out[i].method < out[j].method
	})
	return out
}

func assertBroadcastRouteClassification(t *testing.T, routes []routeKey) {
	t.Helper()
	if len(routes) < 100 {
		t.Fatalf("only %d routes registered", len(routes))
	}
	seen := make(map[routeKey]bool, len(routes))
	haveMethodPath := make(map[string]bool, len(routes))
	broadcastCount := 0
	for _, r := range routes {
		seen[r] = true
		haveMethodPath[r.method+" "+r.pattern] = true
		isBroadcast, classified := apiRouteClassification[r]
		if !classified {
			t.Errorf("unclassified route %s listener %s %s: add it to apiRouteClassification in broadcast_routes_test.go",
				r.listener, r.method, r.pattern)
			continue
		}
		if isBroadcast {
			broadcastCount++
			if !broadcastgate.Covers(r.pattern) {
				t.Errorf("%s listener: %s %s is classified as broadcast/moderation but not covered by broadcastgate.Paths (%v)",
					r.listener, r.method, r.pattern, broadcastgate.Paths)
			}
		} else if broadcastgate.Covers(r.pattern) {
			t.Errorf("%s listener: %s %s is covered by broadcastgate but classified as non-broadcast",
				r.listener, r.method, r.pattern)
		}
	}
	for r := range apiRouteClassification {
		if !seen[r] {
			t.Errorf("stale entry in apiRouteClassification: %s listener %s %s is not registered",
				r.listener, r.method, r.pattern)
		}
	}
	if broadcastCount < 55 {
		t.Errorf("only %d broadcast routes classified", broadcastCount)
	}
	for _, ex := range broadcastgate.Exceptions() {
		if !haveMethodPath[ex.Method+" "+ex.Path] {
			t.Errorf("broadcastgate exception %s %s names no registered route", ex.Method, ex.Path)
		}
	}
}

type noopConnector struct{}

func (noopConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("offline router-only db")
}
func (noopConnector) Driver() driver.Driver { return noopDriver{} }

type noopDriver struct{}

func (noopDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("offline router-only db")
}

// TestBroadcastRouteClassification walks both api listeners built with an offline DB connector and
// verifies every route is explicitly classified in apiRouteClassification and matches broadcastgate.
func TestBroadcastRouteClassification(t *testing.T) {
	var staffKey [32]byte
	_, _ = rand.Read(staffKey[:])
	cfg := config.Config{}
	cfg.Server.AdminListenAddr = "127.0.0.1:8081"
	cfg.Server.AdminHost = "admin.example.test"
	cfg.Server.PublicURL = "https://api.example.test"
	cfg.Server.WebURL = "https://app.example.test"
	cfg.Auth.TokenSecret = "0123456789abcdef0123456789abcdef"
	cfg.Auth.StaffTokenSecret = "fedcba9876543210fedcba9876543210"
	cfg.Auth.StaffSecretKey = base64.RawURLEncoding.EncodeToString(staffKey[:])
	cfg.Billing.Provider = "none"
	cfg.Mail.Driver = "log"

	sqlDB := sql.OpenDB(noopConnector{})
	t.Cleanup(func() { _ = sqlDB.Close() })
	pool := &db.Pool{DB: sqlDB}
	mig := db.NewMigrator(pool, migrations.FS, ".")

	a, stop, err := newAPI(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg, pool, mig, server.BuildInfo{}, apiOverrides{})
	if err != nil {
		t.Fatalf("newAPI: %v", err)
	}
	t.Cleanup(stop)

	assertBroadcastRouteClassification(t, walkAPIListeners(t, a.User, a.Admin))
}
