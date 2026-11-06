// Epic 54 wiring (plugins), Phase 1.
//
//	/internal/plugins/invocations…  TCP, this machine's own processes only (54.04 §2)
//	/plugin-api/v1/*               the unix socket only, never TCP (54.04 §1): served when
//	                               MAKTABA_PLUGIN_API_SOCKET names the socket (the
//	                               supervisor sets it on the home while plugins.host runs)
package router

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"

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
	// Ctx ends the socket's server (nil: never).
	Ctx context.Context
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
	(&pluginsh.Invocations{Tokens: tokens}).Mount(r)
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
