package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

func TestChildEnvSetsStorageForAllRoles(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.Database.URL = "sqlite:///var/lib/maktaba/maktaba.db"

	for _, role := range []Role{RoleAPI, RoleStreaming, RolePipeline} {
		env := ChildEnv(role, cfg)
		if !hasKV(env, "DATABASE_URL", cfg.Database.URL) {
			t.Errorf("role %s: DATABASE_URL not set", role)
		}
		if !hasKV(env, "MAKTABA_DATABASE_URL", cfg.Database.URL) {
			t.Errorf("role %s: MAKTABA_DATABASE_URL not set (pipeline contract)", role)
		}
	}
}

func TestChildEnvRoleSpecificBinds(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	api := ChildEnv(RoleAPI, cfg)
	if !hasKV(api, "MAKTABA_HTTP_ADDR", cfg.Server.Listen) {
		t.Error("api role missing MAKTABA_HTTP_ADDR")
	}
	pipeline := ChildEnv(RolePipeline, cfg)
	if hasKey(pipeline, "MAKTABA_HTTP_ADDR") {
		t.Error("pipeline role should not set MAKTABA_HTTP_ADDR")
	}
	if !hasKey(pipeline, "MAKTABA_PIPELINE_GRPC_ADDR") {
		t.Error("pipeline role should set MAKTABA_PIPELINE_GRPC_ADDR")
	}
}

func TestBuildMissingBinaryIsActionable(t *testing.T) {
	// No t.Parallel(): t.Setenv forbids it.
	t.Setenv("MAKTABA_API_BIN", "/nonexistent/maktaba-api-xyz")
	_, err := Build(RoleAPI, Options{Config: config.Defaults()})
	if !errors.Is(err, ErrBinaryNotFound) {
		t.Fatalf("want ErrBinaryNotFound, got %v", err)
	}
}

func TestLocalAddr(t *testing.T) {
	t.Parallel()
	if got := LocalAddr(":8081", "9999"); got != "127.0.0.1:8081" {
		t.Errorf("LocalAddr(:8081) = %q", got)
	}
	if got := LocalAddr("garbage", "8081"); got != "127.0.0.1:8081" {
		t.Errorf("LocalAddr fallback = %q", got)
	}
}

func hasKV(env []string, key, val string) bool {
	return hasKey(env, key) && contains(env, key+"="+val)
}

func hasKey(env []string, key string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") {
			return true
		}
	}
	return false
}

func contains(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func TestChildEnvLiveTVWiring(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.Server.StreamAddr = "0.0.0.0:8782"
	cfg.Server.PublicURL = "http://tv.lan:8782"

	streaming := ChildEnv(RoleStreaming, cfg)
	// The streaming binary binds MAKTABA_HTTP_ADDR, so stream_addr must
	// land there (it used to be ignored and streaming sat on :8081).
	if !hasKV(streaming, "MAKTABA_HTTP_ADDR", "0.0.0.0:8782") {
		t.Error("streaming role must bind stream_addr via MAKTABA_HTTP_ADDR")
	}
	for _, role := range []Role{RoleAPI, RoleStreaming, RolePipeline} {
		if !hasKV(ChildEnv(role, cfg), "MAKTABA_PUBLIC_BASE_URL", "http://tv.lan:8782") {
			t.Errorf("role %s: MAKTABA_PUBLIC_BASE_URL not set from public_url", role)
		}
	}
	// The api's streaming client speaks gRPC, not the HTTP stream port.
	if !hasKV(ChildEnv(RoleAPI, cfg), "MAKTABA_STREAMING_ADDR", StreamingGRPCAddr) {
		t.Error("api MAKTABA_STREAMING_ADDR must be the streaming gRPC address")
	}
}

func TestChildEnvSharesTheSecretsKey(t *testing.T) {
	t.Setenv("MAKTABA_SECRETS_KEY", "")
	t.Setenv("MAKTABA_CONFIG", filepath.Join(t.TempDir(), "server.toml"))
	want := SecretsKeyPath()
	if filepath.Base(want) != "secrets.key" || filepath.Dir(want) != filepath.Dir(os.Getenv("MAKTABA_CONFIG")) {
		t.Fatalf("SecretsKeyPath = %s", want)
	}
	cfg := config.Defaults()
	for _, role := range []Role{RoleAPI, RoleStreaming, RolePipeline} {
		if !hasKV(ChildEnv(role, cfg), "MAKTABA_SECRETS_KEY", want) {
			t.Errorf("role %s: MAKTABA_SECRETS_KEY not set", role)
		}
	}
	t.Setenv("MAKTABA_SECRETS_KEY", "/own/secrets.key")
	for _, kv := range ChildEnv(RoleAPI, cfg) {
		if strings.HasPrefix(kv, "MAKTABA_SECRETS_KEY=") {
			t.Errorf("an operator's own key path must be kept: %s", kv)
		}
	}
}
