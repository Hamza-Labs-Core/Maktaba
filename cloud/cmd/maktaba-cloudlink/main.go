// Command maktaba-cloudlink is the on-prem → cloud tunnel agent
// (Story 25.7). It runs as a separate process next to the self-hosted
// maktaba-api: it claims the box against the cloud once, then keeps a
// single WSS tunnel up so the cloud edge can reach the loopback API
// without any inbound port-forwarding.
//
// This binary is the previously-missing counterpart to
// cloud/internal/relay (the cloud-side accept handler). Before it, the
// cloud accepted tunnels nobody dialed; this is the dialer.
//
// Configuration is via environment (the on-prem box has no cloud.toml):
//
//	MAKTABA_CLOUD_API     https://api.maktaba.app   (claim REST base)
//	MAKTABA_RELAY_WS       wss://relay.maktaba.app/v1/relay/ws
//	MAKTABA_LOCAL_API      http://127.0.0.1:8080     (loopback to maktaba-api)
//	MAKTABA_CRED_FILE      /var/lib/maktaba/cloudlink.cred
//	MAKTABA_CRED_KEY_HEX   64 hex chars (AES-256 key for cred-at-rest)
//	MAKTABA_CLAIM_CODE     8-char claim code (only on first run)
//	MAKTABA_SERVER_SLUG    desired subdomain slug (only on first run)
//	MAKTABA_SERVER_NAME    human label (only on first run)
//	MAKTABA_ADMIN_ADDR     127.0.0.1:8765            (GET /admin/cloud-link)
package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/cloudlink"
)

var (
	Version = "dev"
	Commit  = "unknown"
	BuiltAt = ""
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("maktaba-cloudlink %s\n  commit %s\n  built  %s\n  go     %s\n",
			Version, Commit, BuiltAt, runtime.Version())
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := loadConfig()
	if err != nil {
		logger.Error("config error", "err", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	creds, err := ensureCredentials(ctx, logger, cfg)
	if err != nil {
		logger.Error("claim/credentials failed", "err", err)
		os.Exit(1)
	}
	logger.Info("cloudlink credentials ready", "slug", creds.Slug)

	sup := &cloudlink.Supervisor{
		Dialer: &cloudlink.Dialer{
			Endpoint: cfg.relayWS,
			ServerID: creds.ServerID,
			Secret:   creds.Secret,
		},
		Proxy: &cloudlink.LocalProxy{BaseURL: cfg.localAPI},
	}

	// Local admin surface: GET /admin/cloud-link.
	adminSrv := &http.Server{
		Addr:              cfg.adminAddr,
		Handler:           adminMux(sup),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if aerr := adminSrv.ListenAndServe(); aerr != nil && !errors.Is(aerr, http.ErrServerClosed) {
			logger.Error("admin listener exited", "err", aerr)
		}
	}()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = adminSrv.Shutdown(sctx)
	}()

	logger.Info("cloudlink supervisor starting", "relay", cfg.relayWS, "local", cfg.localAPI)
	if rerr := sup.Run(ctx); rerr != nil && !errors.Is(rerr, context.Canceled) {
		logger.Error("supervisor exited", "err", rerr)
		os.Exit(1)
	}
	logger.Info("cloudlink stopped")
}

func adminMux(sup *cloudlink.Supervisor) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/admin/cloud-link", cloudlink.AdminHandler(sup))
	return mux
}

type config struct {
	cloudAPI  string
	relayWS   string
	localAPI  string
	credFile  string
	credKey   []byte
	claimCode string
	slug      string
	name      string
	adminAddr string
}

func loadConfig() (config, error) {
	c := config{
		cloudAPI:  envOr("MAKTABA_CLOUD_API", "https://api.maktaba.app"),
		relayWS:   envOr("MAKTABA_RELAY_WS", "wss://relay.maktaba.app/v1/relay/ws"),
		localAPI:  envOr("MAKTABA_LOCAL_API", "http://127.0.0.1:8080"),
		credFile:  envOr("MAKTABA_CRED_FILE", "/var/lib/maktaba/cloudlink.cred"),
		claimCode: os.Getenv("MAKTABA_CLAIM_CODE"),
		slug:      os.Getenv("MAKTABA_SERVER_SLUG"),
		name:      envOr("MAKTABA_SERVER_NAME", "maktaba"),
		adminAddr: envOr("MAKTABA_ADMIN_ADDR", "127.0.0.1:8765"),
	}
	keyHex := os.Getenv("MAKTABA_CRED_KEY_HEX")
	if keyHex == "" {
		return config{}, errors.New("MAKTABA_CRED_KEY_HEX is required (64 hex chars / AES-256)")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return config{}, errors.New("MAKTABA_CRED_KEY_HEX must decode to exactly 32 bytes")
	}
	c.credKey = key
	return c, nil
}

// ensureCredentials loads sealed creds if present, otherwise performs a
// one-time claim redeem and persists the result encrypted-at-rest.
func ensureCredentials(ctx context.Context, logger *slog.Logger, cfg config) (cloudlink.Credentials, error) {
	if creds, err := cloudlink.LoadCredentials(cfg.credFile, cfg.credKey); err == nil {
		return creds, nil
	} else if !os.IsNotExist(err) {
		logger.Warn("existing credential file unreadable; will attempt claim", "err", err)
	}
	if cfg.claimCode == "" || cfg.slug == "" {
		return cloudlink.Credentials{}, errors.New("no stored credentials and MAKTABA_CLAIM_CODE / MAKTABA_SERVER_SLUG not set")
	}
	claimer := &cloudlink.Claimer{Endpoint: cfg.cloudAPI}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	creds, err := claimer.Redeem(cctx, cfg.claimCode, cfg.name, cfg.slug, Version, "")
	if err != nil {
		return cloudlink.Credentials{}, err
	}
	if serr := cloudlink.SaveCredentials(cfg.credFile, cfg.credKey, creds); serr != nil {
		return cloudlink.Credentials{}, fmt.Errorf("persist credentials: %w", serr)
	}
	logger.Info("server claimed and credentials persisted", "server_id", creds.ServerID)
	return creds, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
