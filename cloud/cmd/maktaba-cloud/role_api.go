package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/abuse"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/oauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/token"
	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/entitlement"
	accounth "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/account"
	adminh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/admin"
	authh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/auth"
	billingh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/billing"
	healthh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/health"
	homesh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/homes"
	metricsh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/metrics"
	pushh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/push"
	serversh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/servers"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/homeassert"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/license"
	mailpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	metricspkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/metrics"
	cmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/obs"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/privacy"
	pushpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/push"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/ratelimit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/reports"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/telemetry"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/turnstile"
)

func runAPI(logger *slog.Logger, cfg config.Config, pool *db.Pool, mig *db.Migrator, build server.BuildInfo) {
	a, stop, err := newAPI(logger, cfg, pool, mig, build, apiOverrides{})
	if err != nil {
		logger.Error("api setup", "err", err)
		os.Exit(2)
	}
	defer stop()
	srvs := []*http.Server{httpServer(cfg.Server.ListenAddr, a.User, cfg.Server.ReadTimeout, cfg.Server.WriteTimeout)}
	// the operator console's own listener (story 50.32): without it the console has no address
	if cfg.Server.AdminListenAddr != "" {
		srvs = append(srvs, httpServer(cfg.Server.AdminListenAddr, a.Admin, cfg.Server.ReadTimeout, cfg.Server.WriteTimeout))
	} else {
		logger.Warn("server.admin_listen_addr is not set: the operator console is not served")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.RunAll(ctx, logger, cfg.Server.ShutdownGrace, srvs...); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

// apiOverrides swaps external collaborators for fakes in tests: a
// recording mailer, a fake payment provider, a fake OAuth provider. The
// zero value is production.
type apiOverrides struct {
	Mailer          mailpkg.Mailer
	BillingProvider billingpkg.Provider // replaces cfg.Billing.Provider
	Google          *oauth.GoogleConfig // endpoints only; ids come from cfg
	Apple           *oauth.AppleConfig
	Turnstile       turnstile.Verifier // replaces siteverify (story 50.30)
}

// apiRouters are the api role's two listeners (Epic 50 story 50.32 §1): User for api, app and
// maktaba.tv, with no admin route at all; Admin for admin.maktaba.tv, with /v1/admin/* only.
type apiRouters struct {
	User  *chi.Mux
	Admin *chi.Mux
}

// newAPI builds the api role's routers. stop releases its background
// goroutines.
func newAPI(logger *slog.Logger, cfg config.Config, pool *db.Pool, mig *db.Migrator, build server.BuildInfo, o apiOverrides) (apiRouters, func(), error) {
	// Token signer for access tokens. The secret is required: the api
	// role can't function without it. config.Load layers
	// MAKTABA_CLOUD_TOKEN_SECRET into cfg.Auth.
	secret, err := token.SecretFromEnv(cfg.Auth.TokenSecret)
	if err != nil {
		return apiRouters{}, nil, err
	}
	signer := token.NewSigner(secret)

	// The bot check on the auth pages (story 50.30 §4): on without its secret, the role
	// refuses to start rather than sign people in unchecked.
	tsGuard, err := turnstileGuard(cfg, o.Turnstile)
	if err != nil {
		return apiRouters{}, nil, err
	}

	// The payment provider is not chosen yet; "none" takes no payments.
	payments, err := billingpkg.New(cfg.Billing.Provider)
	if err != nil {
		return apiRouters{}, nil, err
	}
	if o.BillingProvider != nil {
		payments = o.BillingProvider
	}

	users := stores.NewUsers(pool.DB)
	servers := stores.NewServers(pool.DB)
	sess := sessions.New(pool.DB)
	detector := abuse.New(pool.DB)
	auditLog := audit.New(pool.DB)
	var mailer mailpkg.Mailer = mailpkg.LogMailer{Logger: logger}
	if o.Mailer != nil {
		mailer = o.Mailer
	}
	templates := mailpkg.Templates{WebURL: cfg.Server.WebURL}

	// Entitlement signing is optional in development: without a key the
	// entitlement endpoints answer 503.
	var entSigner *entitlement.Signer
	if cfg.Entitlement.PrivateKeyPath != "" {
		s, err := entitlement.LoadSignerFromFile(cfg.Entitlement.PrivateKeyPath)
		if err != nil {
			logger.Warn("entitlement signing disabled", "path", cfg.Entitlement.PrivateKeyPath, "err", err)
		} else if err := entitlement.RegisterKey(context.Background(), pool.DB, s); err != nil {
			return apiRouters{}, nil, err
		} else {
			entSigner = s
		}
	}
	// Linked accounts (Epic 42 story 42.7): home assertions are signed with
	// a key derived from the entitlement key, so the one key file covers
	// both; without it, linked sign-in is off (503) and the rest runs.
	var assertions *homeassert.Signer
	if entSigner != nil {
		assertions = homeassert.DeriveFromSeed(entSigner.Priv.Seed())
	} else {
		logger.Warn("no entitlement key: linked-account sign-in (home assertions) is off")
	}
	// Licenses (Epic 16 story 16.4): a paid plan becomes a license its
	// homes verify, signed with the entitlement key (release builds of
	// the home embed its public half); without it, licensing is off.
	var licenses *license.Issuer
	if entSigner != nil {
		licenses = &license.Issuer{DB: pool.DB, Priv: entSigner.Priv}
	}
	serversDeps := serversh.Deps{Servers: servers, Assertions: assertions, Licenses: licenses,
		ManageURL: strings.TrimRight(cfg.Server.WebURL, "/") + "/billing"}
	homes := &homesh.Deps{Servers: servers, Users: users, Signer: assertions, RelayHost: cfg.Relay.PublicHost,
		WebURL: cfg.Server.WebURL}
	if cfg.Direct.Enabled {
		homes.Direct = stores.NewDirect(pool.DB) // story 50.15: a direct entry once its check passed
	}

	// Push dispatcher — both drivers may be nil in non-prod; the
	// dispatcher will log no-driver and skip those rows safely.
	var apns pushpkg.Driver
	if cfg.APNs.TeamID != "" && cfg.APNs.KeyPath != "" {
		apns = pushpkg.NewAPNsDriver(cfg.APNs.TeamID, cfg.APNs.KeyID, cfg.APNs.KeyPath, cfg.APNs.BundleID)
	}
	var fcm pushpkg.Driver
	if cfg.FCM.ProjectID != "" && cfg.FCM.ServiceAccountPath != "" {
		fcm = pushpkg.NewFCMDriver(cfg.FCM.ProjectID, cfg.FCM.ServiceAccountPath)
	}
	dispatcher := pushpkg.NewDispatcher(pool.DB, apns, fcm)

	// Epic 30 push counters: pushes are sent from this role, so its own
	// collector counts them (Dispatcher.Metrics → RecordPush) and flushes
	// push_sent / push_failed into relay_metrics_raw each minute for the
	// dashboard and export; GET /metrics on this role serves the
	// cumulative counters to Prometheus. Rollups and purges stay with the
	// relay role's runner.
	metricsStore := metricspkg.NewStore(pool.DB)
	pushMetrics := metricspkg.NewCollector()
	dispatcher.Metrics = pushMetrics
	metricsCtx, stopMetrics := context.WithCancel(context.Background())
	go metricspkg.NewRunner(pushMetrics, metricsStore, nil, nil, logger).RunFlushOnly(metricsCtx)

	// Anonymous usage data (Epic 16 story 16.7): this role owns its
	// tables, so it also runs their daily retention sweep (idempotent
	// deletes; several api pods sweeping is harmless).
	telemetryStore := &telemetry.Store{DB: pool.DB}
	go telemetry.RunSweeper(metricsCtx, telemetryStore, telemetry.DefaultRetention, logger)

	// Problem reports homes chose to send (Epic 53 story 53.16): this role owns the table and
	// sweeps it past 90 days. MAKTABA_CLOUD_REPORTS=off answers 503 to new ones (the
	// rollback: homes keep them in their outbox).
	reportStore := &reports.Store{DB: pool.DB}
	go reports.RunSweeper(metricsCtx, reportStore, logger)

	// OAuth flows — each is on only when its client id is configured.
	var googleFlow *oauth.GoogleFlow
	if cfg.OAuthGoogle.ClientID != "" {
		gc := oauth.GoogleConfig{}
		if o.Google != nil {
			gc = *o.Google
		}
		gc.ClientID = cfg.OAuthGoogle.ClientID
		gc.ClientSecret = cfg.OAuthGoogle.ClientSecret
		gc.RedirectURL = cfg.Server.PublicURL + "/v1/auth/oauth/google/callback"
		googleFlow = oauth.NewGoogleFlow(gc)
	}
	var appleFlow *oauth.AppleFlow
	if cfg.OAuthApple.ClientID != "" {
		ac := oauth.AppleConfig{}
		if o.Apple != nil {
			ac = *o.Apple
		}
		ac.TeamID, ac.KeyID, ac.ClientID, ac.KeyPath = cfg.OAuthApple.TeamID, cfg.OAuthApple.KeyID, cfg.OAuthApple.ClientID, cfg.OAuthApple.KeyPath
		ac.RedirectURL = cfg.Server.PublicURL + "/v1/auth/oauth/apple/callback"
		appleFlow = oauth.NewAppleFlow(ac)
	}

	// The account routes, shared by both listeners until 50.33 (ConsoleSignIn).
	authDeps := &authh.Deps{
		Users:     users,
		Sessions:  sess,
		Signer:    signer,
		Tokens:    stores.NewMailTokens(pool.DB),
		Mailer:    mailer,
		Templates: templates,
		Abuse:     detector,
		Audit:     auditLog,
		Logger:    logger,
		Google:    googleFlow,
		Apple:     appleFlow,
		WebURL:    cfg.Server.WebURL,
		// Host-only: a Domain=.maktaba.tv cookie would also go to
		// every home server behind *.relay.maktaba.tv.
		CookieDomain:     "",
		Secure:           true,
		Turnstile:        tsGuard,
		TurnstileSiteKey: cfg.Turnstile.SiteKey,
	}
	accountDeps := &accounth.Deps{Users: users, Sessions: sess, Audit: auditLog, Logger: logger}
	// Build the router. Order matters: health probes mount on the
	// outer router before the auth-required group so they don't need
	// a token.
	// The client IP is resolved first, the access log included: CF-Connecting-IP only from a
	// peer in server.trusted_proxies (Epic 50 story 50.31).
	// The user listener answers server.user_hosts only and has no admin route (story 50.32).
	r := server.NewRouter(logger, pool, mig, build, server.DefaultOrigins(cfg),
		server.WithTrustedProxies(cfg.Server.TrustedProxies), server.WithUserHosts(cfg.Server.UserHosts))
	adminR := server.NewAdminRouter(logger, pool, mig, build, cfg.Server.AdminHost,
		cfg.Server.AdminOriginOrDefault(), cfg.Server.TrustedProxies)
	// NewRouter has already registered the health probes, and chi
	// refuses Use after a route, so the app routes and their middleware
	// live in a group.
	r.Group(func(app chi.Router) {
		// Per-IP rate limits on the unauthenticated auth routes: burst 30,
		// refill 5/s — plenty for the sign-in form, harsh for credential
		// stuffing — and burst 5, one per 12 s for the routes that send mail.
		// Per-account lockout (10 failures / 15 min) lives in the login
		// handler.
		app.Use(authRateLimit(ratelimit.NewLimiter(30, 5), authh.RateLimitedPaths))
		app.Use(authRateLimit(ratelimit.NewLimiter(5, 1.0/12), authh.MailPaths))
		app.Use(blockedIPs(detector, logger))
		// a claim code is 8 characters: its redeem is limited per IP too
		app.Use(authRateLimit(ratelimit.NewLimiter(30, 5), []string{"/v1/servers/claims/redeem"}))

		// A home's agent: the claim code or the server's own secret is its
		// credential, never a user token.
		serversh.MountAgent(app, serversDeps)
		reports.MountAgent(app, &reports.AgentDeps{Store: reportStore, Servers: servers, Logger: logger,
			Refuse: strings.EqualFold(os.Getenv("MAKTABA_CLOUD_REPORTS"), "off")})
		homesh.MountPublic(app, homes)

		// Anonymous usage data from homes: no account, no server secret.
		// The address is read for the rate limit only, never stored.
		telemetry.Mount(app, telemetry.NewHandler(telemetryStore, logger))

		authh.Mount(app, authDeps)

		billing := &billingh.Deps{
			DB: pool.DB, Provider: payments, Users: users, Sessions: sess, Abuse: detector, Audit: auditLog,
			Mailer: mailer, Logger: logger, Entitlements: entSigner,
			WebURL: cfg.Server.WebURL,
		}
		// Public billing routes: the catalog, the entitlement key, and the
		// payment-provider webhook (verified by the provider, not our token).
		billingh.MountPublic(app, billing)

		// Authenticated group.
		app.Group(func(g chi.Router) {
			g.Use(middleware.RequireUser(signer, middleware.UserStatus(pool.DB)))
			accounth.Mount(g, accountDeps)
			serversh.MountUser(g, serversDeps)
			homesh.MountUser(g, homes)
			(&healthh.Deps{DB: pool.DB}).Mount(g)
			billingh.Mount(g, billing)
			pushh.Mount(g, pushh.Deps{DB: pool.DB, Dispatcher: dispatcher})

		})
	})

	// Prometheus scrape of this role's push counters (aggregate only, like
	// the relay's /metrics).
	r.Get("/metrics", metricspkg.PrometheusHandler(pushMetrics, obs.WriteMetrics))

	// The operator console's listener (story 50.32): every /v1/admin/* route, behind a signed-in
	// user whose cloud role in the database is service_admin (50.33 replaces this with staff
	// sign-in on /v1/staff/*). Nothing else is mounted here.
	adminGate := middleware.RequireServiceAdmin(users, auditLog, logger)
	live := metricsh.LiveFromStore(metricsStore, time.Now)
	// Until 50.33's staff sign-in exists, the console signs in with the account routes it has
	// always used, and only those (ConsoleSignIn), on its own host: the refresh cookie is
	// host-only, so a console session is never a session on the user names, and the console
	// listener's Origin rule covers these POSTs too. 50.33 replaces them with /v1/staff/*.
	adminR.Group(func(g chi.Router) {
		g.Use(authRateLimit(ratelimit.NewLimiter(30, 5), authh.RateLimitedPaths))
		g.Use(blockedIPs(detector, logger))
		g.Post("/v1/auth/login", authDeps.Login)
		g.Post("/v1/auth/refresh", authDeps.Refresh)
		g.Post("/v1/auth/logout", authDeps.Logout)
	})
	adminR.Group(func(g chi.Router) {
		g.Use(middleware.RequireUser(signer, middleware.UserStatus(pool.DB)))
		g.Get("/v1/account/me", accountDeps.Me)
		// Service-admin console: every /v1/admin/* route checks the cloud
		// role in the database (never an email domain or a home role).
		adminh.Mount(g, &adminh.Deps{
			DB: pool.DB, Users: users, Sessions: sess, Abuse: detector, Audit: auditLog,
			Logger: logger, Gate: adminGate, LiveTunnels: live, Licenses: licenses,
			// the cloud's log index on W (53.17), as the forwarder reaches it
			Logs: &adminh.LogsSource{URL: os.Getenv(EnvLogsURL), Token: logsToken()},
		})

		// the problem reports homes sent, for the service admin only (53.16)
		reports.MountAdmin(g, &reports.AdminDeps{Store: reportStore, DB: pool.DB, Logger: logger, Gate: adminGate})

		// Epic 30 — relay-analytics dashboard + export + GDPR endpoints.
		// Reads the relay_metrics_* tables the relay role writes (same DB);
		// the api role owns auth, so the operator-gated surface lives here.
		metricsh.Mount(g, metricsh.Deps{
			DB:    pool.DB,
			Gate:  adminGate,
			Audit: auditLog,
			Store: metricsStore,
			// the relay's registry lives in the relay role's process; read
			// the gauges it flushes each minute
			Live:     live,
			Deletion: privacy.NewDataSubjectService(pool.DB),
		})
	})

	return apiRouters{User: r, Admin: adminR}, stopMetrics, nil
}

// ConsoleSignIn are the account routes the console listener also serves until 50.33's staff
// sign-in replaces them: sign in, refresh, sign out, and who is signed in.
var ConsoleSignIn = map[string]bool{
	"POST /v1/auth/login": true, "POST /v1/auth/refresh": true, "POST /v1/auth/logout": true,
	"GET /v1/account/me": true,
}

// authRateLimit applies l, keyed by client IP, to the given paths only.
func authRateLimit(l *ratelimit.Limiter, paths []string) func(http.Handler) http.Handler {
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	return l.Middleware(func(req *http.Request) string {
		if set[req.URL.Path] {
			return ratelimit.IPKey(req)
		}
		return ""
	})
}

// blockedIPs refuses the auth routes to a blocklisted client IP.
func blockedIPs(d *abuse.Detector, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := cmw.GetClientIP(r.Context())
			if ip == "" || !strings.HasPrefix(r.URL.Path, "/v1/auth/") {
				next.ServeHTTP(w, r)
				return
			}
			blocked, err := d.IsBlocked(r.Context(), abuse.Subject{Kind: "ip", ID: ip})
			if err != nil {
				logger.Warn("blocklist check", "err", err)
			}
			if blocked {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"blocked"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// turnstileGuard is the auth pages' bot check from the config (story 50.30 §4): nil when off;
// refused when on without its secret, or without a web origin to ask.
func turnstileGuard(cfg config.Config, fake turnstile.Verifier) (*turnstile.Guard, error) {
	t := cfg.Turnstile
	if !t.Enabled {
		return nil, nil
	}
	if t.Secret == "" && fake == nil {
		return nil, fmt.Errorf("turnstile.enabled is on but MAKTABA_CLOUD_TURNSTILE_SECRET is not set")
	}
	if len(t.WebOrigins) == 0 {
		return nil, fmt.Errorf("turnstile.enabled is on but turnstile.web_origins (or server.web_url) names no web origin")
	}
	hosts := make([]string, 0, len(t.WebOrigins))
	for _, o := range t.WebOrigins {
		u, err := url.Parse(o)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("turnstile.web_origins: %q is not an origin", o)
		}
		hosts = append(hosts, u.Hostname())
	}
	var v turnstile.Verifier = &turnstile.Client{Secret: t.Secret, Hostnames: hosts}
	if fake != nil {
		v = fake
	}
	return &turnstile.Guard{Enabled: true, Verifier: v, WebOrigins: t.WebOrigins,
		ClientIP: func(r *http.Request) string { return cmw.GetClientIP(r.Context()) }}, nil
}
