package supervisor

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

func TestChildEnvSetsStorageForAllRoles(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.Database.URL = "sqlite:///var/lib/maktaba/maktaba.db"

	for _, role := range []Role{RoleAPI, RoleStreaming, RolePipeline} {
		env := childEnvOK(role, cfg)
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
	api := childEnvOK(RoleAPI, cfg)
	if !hasKV(api, "MAKTABA_HTTP_ADDR", cfg.Server.Listen) {
		t.Error("api role missing MAKTABA_HTTP_ADDR")
	}
	pipeline := childEnvOK(RolePipeline, cfg)
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

	streaming := childEnvOK(RoleStreaming, cfg)
	// The streaming binary binds MAKTABA_HTTP_ADDR, so stream_addr must
	// land there (it used to be ignored and streaming sat on :8081).
	if !hasKV(streaming, "MAKTABA_HTTP_ADDR", "0.0.0.0:8782") {
		t.Error("streaming role must bind stream_addr via MAKTABA_HTTP_ADDR")
	}
	for _, role := range []Role{RoleAPI, RoleStreaming, RolePipeline} {
		if !hasKV(childEnvOK(role, cfg), "MAKTABA_PUBLIC_BASE_URL", "http://tv.lan:8782") {
			t.Errorf("role %s: MAKTABA_PUBLIC_BASE_URL not set from public_url", role)
		}
	}
	// The api's streaming client speaks gRPC, not the HTTP stream port.
	if !hasKV(childEnvOK(RoleAPI, cfg), "MAKTABA_STREAMING_ADDR", StreamingGRPCAddr) {
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
		if !hasKV(childEnvOK(role, cfg), "MAKTABA_SECRETS_KEY", want) {
			t.Errorf("role %s: MAKTABA_SECRETS_KEY not set", role)
		}
	}
	t.Setenv("MAKTABA_SECRETS_KEY", "/own/secrets.key")
	for _, kv := range childEnvOK(RoleAPI, cfg) {
		if strings.HasPrefix(kv, "MAKTABA_SECRETS_KEY=") {
			t.Errorf("an operator's own key path must be kept: %s", kv)
		}
	}
}

// A home without a JWT pair makes one, and the API gets it: a native app's sign-in (the TV
// apps, the native Player and Remote) answered 500 without one. A pair already there (a
// switch of the home handed it over) is kept, and an operator's own keys win.
func TestChildEnvGivesTheAPIAJWTPair(t *testing.T) {
	jwtKeyBits = 1024
	t.Cleanup(func() { jwtKeyBits = 2048 })
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", "")
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", "")
	t.Setenv("MAKTABA_CONFIG", filepath.Join(t.TempDir(), "server.toml"))
	cfg := config.Defaults()

	// none yet: the home makes a pair the api can read, private to its owner
	env := childEnvOK(RoleAPI, cfg)
	priv, pub := JWTKeyPaths()
	privPEM, err1 := os.ReadFile(priv)
	pubPEM, err2 := os.ReadFile(pub)
	if err1 != nil || err2 != nil {
		t.Fatalf("no pair made: %v %v", err1, err2)
	}
	if !hasKV(env, "MAKTABA_JWT_PRIVATE_KEY_PEM", string(privPEM)) || !hasKV(env, "MAKTABA_JWT_PUBLIC_KEY_PEM", string(pubPEM)) {
		t.Fatal("the API does not get the pair made")
	}
	if b, _ := pem.Decode(privPEM); b == nil || b.Type != "PRIVATE KEY" {
		t.Fatalf("private key: %q", privPEM)
	} else if _, err := x509.ParsePKCS8PrivateKey(b.Bytes); err != nil {
		t.Fatal(err)
	}
	if b, _ := pem.Decode(pubPEM); b == nil || b.Type != "PUBLIC KEY" {
		t.Fatalf("public key: %q", pubPEM)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(priv); fi.Mode().Perm() != 0o600 {
			t.Fatalf("private key mode %v", fi.Mode().Perm())
		}
	}
	// made once: the next start keeps it (tokens signed with it stay good)
	childEnvOK(RoleAPI, cfg)
	if again, _ := os.ReadFile(priv); string(again) != string(privPEM) {
		t.Fatal("the pair was made anew")
	}
	for _, kv := range childEnvOK(RolePipeline, cfg) {
		if strings.HasPrefix(kv, "MAKTABA_JWT_") {
			t.Fatalf("the pipeline got a JWT key: %s", kv)
		}
	}

	// a pair a switch of the home handed over is the one the API gets
	if err := os.WriteFile(priv, []byte("PRIVATE PEM"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pub, []byte("PUBLIC PEM"), 0o600); err != nil {
		t.Fatal(err)
	}
	env = childEnvOK(RoleAPI, cfg)
	if !hasKV(env, "MAKTABA_JWT_PRIVATE_KEY_PEM", "PRIVATE PEM") || !hasKV(env, "MAKTABA_JWT_PUBLIC_KEY_PEM", "PUBLIC PEM") {
		t.Fatal("the API does not get the handed-over JWT keys")
	}

	// half a pair is never completed with a new private key
	if err := os.Remove(pub); err != nil {
		t.Fatal(err)
	}
	if _, err := ChildEnv(RoleAPI, cfg); err == nil {
		t.Fatal("half a pair: no error")
	}

	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", "OWN")
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", "OWN PUB")
	for _, kv := range childEnvOK(RoleAPI, cfg) {
		if strings.HasPrefix(kv, "MAKTABA_JWT_") {
			t.Errorf("an operator's own JWT keys must be kept: %s", kv)
		}
	}
}
