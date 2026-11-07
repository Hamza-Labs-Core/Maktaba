// Epic 54 wiring (plugins), Phase 1.
//
//	/api/plugins/*                 the admin routes (54.07, 54.08 §3): admin-only
//	/internal/plugins/invocations…  TCP, this machine's own processes only (54.04 §2)
//	/internal/plugins/breaker      TCP, this machine's pipeline only (54.09 §2)
//	/plugin-api/v1/*               the unix socket only, never TCP (54.04 §1): served when
//	                               MAKTABA_PLUGIN_API_SOCKET names the socket (the
//	                               supervisor sets it on the home while plugins.host runs)
package router

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	pluginsh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
)

// P54Deps bundles the Epic 54 routes.
type P54Deps struct {
	DB     *sql.DB
	Logger *slog.Logger
	// DataDir is <data>: plugin artifacts go to <data>/plugins/artifacts.
	DataDir string
	// Socket is the plugin API's unix socket ("": MAKTABA_PLUGIN_API_SOCKET; none: not served).
	Socket string
	// Ctx ends the socket's server and the catalog's watch (nil: never).
	Ctx context.Context
	// Components is the components service (MountP51's): the lifecycle switches plugins
	// through it, and is its PluginHooks. nil: no admin routes.
	Components *components.Service
	// DSN is the catalog watch's listener ("": polled).
	DSN string
	// Version is this server's (a plugin's maktaba range is checked against it).
	Version string
	// Sup puts packages on disk (nil: the supervisor this API runs under).
	Sup plugins.Supervisor
}

// MountP54 mounts the invocation routes on r and, when a socket is named, serves the
// plugin API on it. It answers the plugin API server (nil DB: nothing).
func MountP54(r chi.Router, d P54Deps) *plugins.Server {
	if d.DB == nil {
		return nil
	}
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	tokens := plugins.NewTokens(d.DB)
	inv := &pluginsh.Invocations{Tokens: tokens}
	if d.Components != nil {
		sup := d.Sup
		if sup == nil {
			if s := plugins.SupervisorFromEnv(); s != nil {
				sup = s
			}
		}
		l := &plugins.Lifecycle{DB: d.DB, Components: d.Components, Sup: sup, Version: d.Version, Logger: logger}
		if d.DataDir != "" {
			l.ArtifactsDir = filepath.Join(d.DataDir, "plugins", "artifacts")
		}
		d.Components.Plugins = l
		inv.Trip = func(r *http.Request, id, reason string) (bool, error) { return l.Trip(r.Context(), id, reason) }
		accounts := users.New(d.DB)
		(&pluginsh.Admin{L: l, Logger: logger, VerifyPassword: func(ctx context.Context, id, pw string) error {
			u, err := accounts.GetByID(ctx, id)
			if err != nil {
				return err
			}
			return u.VerifyPassword(pw)
		}}).Mount(r)
		ctx := d.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		// story 54.37 §2: the live catalog has the installed plugins' entries
		go plugins.WatchCatalog(ctx, d.DB, d.DSN, logger)
	}
	inv.Mount(r)
	s := &plugins.Server{DB: d.DB, Tokens: tokens, Logger: logger, Media: &plugins.Media{FFmpeg: os.Getenv("MAKTABA_FFMPEG")}}
	if d.DataDir != "" {
		s.ArtifactsDir = filepath.Join(d.DataDir, "plugins", "artifacts")
	}
	sock := d.Socket
	if sock == "" {
		sock = os.Getenv("MAKTABA_PLUGIN_API_SOCKET")
	}
	if sock != "" {
		ctx := d.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		go func() {
			if err := plugins.ListenUnix(ctx, sock, s); err != nil {
				logger.Error("plugin_api_listen_failed", "socket", sock, "err", err)
			}
		}()
		logger.Info("plugin_api_listening", "socket", sock)
	}
	return s
}
