package supervisor

import (
	"fmt"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// ChildEnv translates a Config into the environment variables a given
// role's process already understands. This is the one place that knows
// the mapping from the human-facing server.toml to the per-service env
// contract — keeping the role binaries themselves unchanged.
//
// Shared wiring (so api/streaming/pipeline agree on where each other
// lives) is applied to every role; role-specific bind addresses are
// only set for the role that owns them.
func ChildEnv(role Role, cfg config.Config) []string {
	env := []string{}
	add := func(k, v string) {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}

	// Storage: the api + streaming read DATABASE_URL; the pipeline reads
	// MAKTABA_DATABASE_URL. Set both so a single [database].url drives
	// the whole stack regardless of which service consumes it.
	add("DATABASE_URL", cfg.Database.URL)
	add("MAKTABA_DATABASE_URL", cfg.Database.URL)

	// Auto-run migrations before the api binds (the api honours this),
	// so a fresh single-binary install doesn't need a separate
	// `migrate up` step before the first `serve`.
	add("MAKTABA_AUTO_MIGRATE", "true")

	// Media roots — comma-joined for the scanner.
	add("MAKTABA_MEDIA_ROOTS", strings.Join(cfg.Media.Roots, ","))

	// Transcription backend/model for the pipeline workers.
	add("MAKTABA_TRANSCRIPTION_BACKEND", cfg.Transcription.Backend)
	add("MAKTABA_TRANSCRIPTION_MODEL", cfg.Transcription.Model)
	add("MAKTABA_MODELS_DIR", cfg.Server.DataDir+"/models")

	// Cross-service discovery: the api needs to reach streaming +
	// pipeline over gRPC. These default ports mirror the compose stack.
	add("MAKTABA_STREAMING_ADDR", localAddr(cfg.Server.StreamAddr, "8081"))
	add("MAKTABA_PIPELINE_ADDR", "127.0.0.1:9090")

	switch role {
	case RoleAPI:
		add("MAKTABA_HTTP_ADDR", cfg.Server.Listen)
		add("MAKTABA_ADMIN_ADDR", cfg.Server.AdminAddr)
	case RoleStreaming:
		add("MAKTABA_STREAMING_HTTP_ADDR", cfg.Server.StreamAddr)
		add("MAKTABA_ADMIN_ADDR", ":9101")
	case RolePipeline:
		// The pipeline exposes its gRPC server so the api can call it.
		add("MAKTABA_PIPELINE_GRPC_ADDR", "127.0.0.1:9090")
	}

	return env
}

// localAddr turns a bind spec like ":8081" or "0.0.0.0:8081" into a
// dialable 127.0.0.1:<port> address for intra-host gRPC/HTTP. Falls back
// to the supplied default port when the spec has no parseable port.
func localAddr(bind, defaultPort string) string {
	port := defaultPort
	if i := strings.LastIndexByte(bind, ':'); i >= 0 && i+1 < len(bind) {
		port = bind[i+1:]
	}
	return fmt.Sprintf("127.0.0.1:%s", port)
}
