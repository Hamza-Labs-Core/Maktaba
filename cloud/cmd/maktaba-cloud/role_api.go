package main

import (
	"context"
	"log/slog"
	"net/http"
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
	mailpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	metricspkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/metrics"
	cmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/privacy"
	pushpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/push"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/ratelimit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

func runAPI(logger *slog.Logger, cfg config.Config, pool *db.Pool, mig *db.Migrator, build server.BuildInfo) {
	r, stop, err := newAPI(logger, cfg, pool, mig, build, apiOverrides{})
	if err != nil {
		logger.Error("api setup", "err", err)
		os.Exit(2)
	}
	defer stop()
	srv := httpServer(cfg.Server.ListenAddr, r, cfg.Server.ReadTimeout, cfg.Server.WriteTimeout)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Run(ctx, logger, srv, cfg.Server.ShutdownGrace); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

// apiOverrides swaps external collaborators for fakes in tests: a
// recording mailer, a fake Stripe API, a fake OAuth provider. The zero
// value is production.
type apiOverrides struct {
	Mailer        mailpkg.Mailer
	StripeAPIBase string
	Google        *oauth.GoogleConfig // endpoints only; ids come from cfg
	Apple         *oauth.AppleConfig
}

// newAPI builds the api role's router. stop releases its background
// goroutines.
func newAPI(logger *slog.Logger, cfg config.Config, pool *db.Pool, mig *db.Migrator, build server.BuildInfo, o apiOverrides) (*chi.Mux, func(), error) {
	// Token signer for access tokens. The secret is required: the api
	// role can't function without it. config.Load layers
	// MAKTABA_CLOUD_TOKEN_SECRET into cfg.Auth.
	secret, err := token.SecretFromEnv(cfg.Auth.TokenSecret)
	if err != nil {
		return nil, nil, err
	}
	signer := token.NewSigner(secret)

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
			return nil, nil, err
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
	serversDeps := serversh.Deps{Servers: servers, Assertions: assertions}
	homes := &homesh.Deps{Servers: servers, Users: users, Signer: assertions, RelayHost: cfg.Relay.PublicHost}

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

	stripe := billingpkg.NewStripeClient(cfg.Stripe.SecretKey)
	if o.StripeAPIBase != "" {
		stripe.APIBase = o.StripeAPIBase
	}

	// Build the router. Order matters: health probes mount on the
	// outer router before the auth-required group so they don't need
	// a token.
	r := server.NewRouter(logger, pool, mig, build, server.DefaultOrigins(cfg))
	// NewRouter has already registered the health probes, and chi
	// refuses Use after a route, so the app routes and their middleware
	// live in a group.
	r.Group(func(app chi.Router) {
		app.Use(cmw.WithClientIP(cfg.Server.TrustProxyHeaders))

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
		homesh.MountPublic(app, homes)

		authh.Mount(app, &authh.Deps{
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
			// Host-only: a Domain=.maktaba.app cookie would also go to
			// every home server behind *.relay.maktaba.app.
			CookieDomain: "",
			Secure:       true,
		})

		billing := &billingh.Deps{
			DB: pool.DB, Stripe: stripe, Users: users, Sessions: sess, Abuse: detector, Audit: auditLog,
			Mailer: mailer, Logger: logger, Entitlements: entSigner,
			WebhookSecret:      cfg.Stripe.WebhookSecret,
			WebURL:             cfg.Server.WebURL,
			PriceIDProMonth:    cfg.Stripe.PriceProMonthly,
			PriceIDFamilyMonth: cfg.Stripe.PriceFamilyMonthly,
		}
		// Public billing routes: the catalog, the entitlement key, and the
		// Stripe webhook (authenticated by its signature, not our token).
		billingh.MountPublic(app, billing)

		adminGate := middleware.RequireServiceAdmin(users, auditLog, logger)
		live := metricsh.LiveFromStore(metricsStore, time.Now)

		// Authenticated group.
		app.Group(func(g chi.Router) {
			g.Use(middleware.RequireUser(signer, middleware.UserStatus(pool.DB)))
			accounth.Mount(g, &accounth.Deps{Users: users, Sessions: sess, Audit: auditLog, Logger: logger})
			serversh.MountUser(g, serversDeps)
			homesh.MountUser(g, homes)
			(&healthh.Deps{DB: pool.DB}).Mount(g)
			billingh.Mount(g, billing)
			pushh.Mount(g, pushh.Deps{DB: pool.DB, Dispatcher: dispatcher})

			// Service-admin console: every /v1/admin/* route checks the cloud
			// role in the database (never an email domain or a home role).
			adminh.Mount(g, &adminh.Deps{
				DB: pool.DB, Users: users, Sessions: sess, Abuse: detector, Audit: auditLog,
				Logger: logger, Gate: adminGate, LiveTunnels: live,
			})

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
	})

	// Prometheus scrape of this role's push counters (aggregate only, like
	// the relay's /metrics).
	r.Get("/metrics", metricspkg.PrometheusHandler(pushMetrics))

	return r, stopMetrics, nil
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
