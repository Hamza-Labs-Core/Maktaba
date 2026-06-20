package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/abuse"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/oauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/token"
	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	accounth "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/account"
	adminh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/admin"
	authh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/auth"
	billingh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/billing"
	healthh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/health"
	metricsh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/metrics"
	pushh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/push"
	serversh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/servers"
	metricspkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/metrics"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/privacy"
	pushpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/push"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/ratelimit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

func runAPI(logger *slog.Logger, cfg config.Config, pool *db.Pool, mig *db.Migrator, build server.BuildInfo) {
	// Token signer for access tokens. The secret is required; we fail
	// loud on missing because the api role can't function without it.
	// config.Load layers MAKTABA_CLOUD_TOKEN_SECRET into cfg.Auth, so
	// the env var remains the injection point but config is the single
	// source of truth.
	secret, err := token.SecretFromEnv(cfg.Auth.TokenSecret)
	if err != nil {
		logger.Error("token secret", "err", err)
		os.Exit(2)
	}
	signer := token.NewSigner(secret)

	users := stores.NewUsers(pool.DB)
	servers := stores.NewServers(pool.DB)
	sess := sessions.New(pool.DB)
	_ = abuse.New(pool.DB) // wired into rate-limiter callbacks in a follow-up story.

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

	// OAuth flows — both optional in dev, validator catches missing
	// config when at least one is required.
	var googleFlow *oauth.GoogleFlow
	if cfg.OAuthGoogle.ClientID != "" {
		googleFlow = oauth.NewGoogleFlow(oauth.GoogleConfig{
			ClientID:     cfg.OAuthGoogle.ClientID,
			ClientSecret: cfg.OAuthGoogle.ClientSecret,
			RedirectURL:  cfg.Server.PublicURL + "/v1/auth/oauth/google/callback",
		}, nil)
	}
	var appleFlow *oauth.AppleFlow
	if cfg.OAuthApple.ClientID != "" {
		appleFlow = oauth.NewAppleFlow(oauth.AppleConfig{
			TeamID:      cfg.OAuthApple.TeamID,
			KeyID:       cfg.OAuthApple.KeyID,
			ClientID:    cfg.OAuthApple.ClientID,
			KeyPath:     cfg.OAuthApple.KeyPath,
			RedirectURL: cfg.Server.PublicURL + "/v1/auth/oauth/apple/callback",
		})
	}

	stripe := billingpkg.NewStripeClient(cfg.Stripe.SecretKey)

	// Build the router. Order matters: health probes mount on the
	// outer router before the auth-required group so they don't need
	// a token.
	r := server.NewRouter(logger, pool, mig, build, server.DefaultOrigins(cfg))

	// Per-IP rate limit on unauthenticated routes. Burst 30, refill
	// 5/sec — plenty for the login form, harsh for credential stuffing.
	rl := ratelimit.NewLimiter(30, 5)
	r.Use(rl.Middleware(func(req *http.Request) string {
		if req.URL.Path == "/v1/auth/login" || req.URL.Path == "/v1/auth/register" {
			return ratelimit.IPKey(req)
		}
		return ""
	}))

	authh.Mount(r, authh.Deps{
		Users:        users,
		Sessions:     sess,
		Signer:       signer,
		Logger:       logger,
		Google:       googleFlow,
		Apple:        appleFlow,
		CookieDomain: extractCookieDomain(cfg.Server.PublicURL),
		Secure:       true,
	})

	// Authenticated group.
	r.Group(func(g chi.Router) {
		g.Use(middleware.RequireUser(signer))
		accounth.Mount(g, accounth.Deps{Users: users, Sessions: sess})
		serversh.Mount(g, serversh.Deps{Servers: servers})
		(&healthh.Deps{DB: pool.DB}).Mount(g)
		billingh.Mount(g, billingh.Deps{
			DB: pool.DB, Stripe: stripe, Users: users,
			WebhookSecret:      cfg.Stripe.WebhookSecret,
			PublicURL:          cfg.Server.PublicURL,
			PriceIDProMonth:    os.Getenv("MAKTABA_CLOUD_PRICE_PRO"),
			PriceIDFamilyMonth: os.Getenv("MAKTABA_CLOUD_PRICE_FAMILY"),
		})
		pushh.Mount(g, pushh.Deps{DB: pool.DB, Dispatcher: dispatcher})
		adminh.Mount(g, adminh.Deps{DB: pool.DB, Users: users, AllowedDomain: cfg.Admin.AllowedEmailDomain})

		// Epic 30 — relay-analytics dashboard + export + GDPR endpoints.
		// Reads the relay_metrics_* tables the relay role writes (same DB);
		// the api role owns auth, so the operator-gated surface lives here.
		metricsh.Mount(g, metricsh.Deps{
			DB:            pool.DB,
			Users:         users,
			AllowedDomain: cfg.Admin.AllowedEmailDomain,
			Store:         metricspkg.NewStore(pool.DB),
			Deletion:      privacy.NewDataSubjectService(pool.DB),
		})
	})

	// The Stripe webhook must NOT require auth — Stripe doesn't carry
	// our bearer tokens. It also needs raw-body access for signature
	// verification; mount it outside the auth group.
	r.Post("/v1/billing/webhook", (&billingh.Deps{
		DB: pool.DB, WebhookSecret: cfg.Stripe.WebhookSecret,
	}).Webhook)

	srv := httpServer(cfg.Server.ListenAddr, r, cfg.Server.ReadTimeout, cfg.Server.WriteTimeout)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Run(ctx, logger, srv, cfg.Server.ShutdownGrace); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
	_ = time.Now
}

func extractCookieDomain(publicURL string) string {
	// "https://api.maktaba.app" → ".maktaba.app" so the cookie is
	// shared with web.maktaba.app. We do a tiny suffix split rather
	// than pulling net/url for one host extraction.
	const prefix = "https://"
	host := publicURL
	if len(host) >= len(prefix) && host[:len(prefix)] == prefix {
		host = host[len(prefix):]
	}
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == '.' {
			// Find the dot before this one — last 2 labels.
			for j := i - 1; j >= 0; j-- {
				if host[j] == '.' {
					return host[j:]
				}
			}
			return "." + host
		}
	}
	return ""
}
