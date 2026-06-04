package supervisor

import (
	"errors"
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
	if got := localAddr(":8081", "9999"); got != "127.0.0.1:8081" {
		t.Errorf("localAddr(:8081) = %q", got)
	}
	if got := localAddr("garbage", "8081"); got != "127.0.0.1:8081" {
		t.Errorf("localAddr fallback = %q", got)
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
