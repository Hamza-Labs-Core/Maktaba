package supervisor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// relayHome isolates the keys directory and makes this machine a home.
func relayHome(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MAKTABA_CONFIG", filepath.Join(dir, "server.toml"))
	t.Setenv("MAKTABA_ROLE", "")
	prev := nodeConfig
	t.Cleanup(func() { nodeConfig = prev })
	nodeConfig = func() config.NodeConfig { return config.NodeConfig{} }
	var cfg config.Config
	cfg.Server.Listen = "0.0.0.0:8082"
	return cfg
}

// The relay agent runs on the home only, while [cloud] is enabled and the home has its
// credentials, or a claim code and slug to get them.
func TestTheRelayRunsOnTheHomeWithItsCredentials(t *testing.T) {
	cfg := relayHome(t)
	if RelayWanted(cfg) {
		t.Fatal("[cloud] off, yet the relay runs")
	}
	cfg.Cloud.Enabled = true
	if RelayWanted(cfg) {
		t.Fatal("no credentials and no claim, yet the relay runs")
	}
	cfg.Cloud.ClaimCode, cfg.Cloud.Slug = "ABCD1234", "my-library"
	if !RelayWanted(cfg) {
		t.Fatal("a claim to make, yet the relay does not run")
	}
	cfg.Cloud.ClaimCode, cfg.Cloud.Slug = "", ""
	if err := os.WriteFile(RelayCredPath(), []byte("sealed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !RelayWanted(cfg) {
		t.Fatal("credentials kept, yet the relay does not run")
	}
	// a machine joined to a home runs none: the home's tunnel is the home's
	nodeConfig = func() config.NodeConfig { return config.NodeConfig{DatabaseURL: "postgres://n@home/maktaba"} }
	if RelayWanted(cfg) {
		t.Fatal("a joined machine runs the relay")
	}
	// the API checks the agent's state once a switch made this machine the home
	nodeConfig = func() config.NodeConfig { return config.NodeConfig{} }
	if m := envMap(childEnvOK(RoleAPI, cfg)); m["MAKTABA_CLOUDLINK_STATUS_URL"] != "http://127.0.0.1:8765/admin/cloud-link" {
		t.Fatalf("the API's relay status address: %q", m["MAKTABA_CLOUDLINK_STATUS_URL"])
	}
}

// The agent's key is made once (0600) and kept; it reaches this machine's API on
// loopback; the claim goes to its first run only.
func TestTheRelaysEnvironment(t *testing.T) {
	cfg := relayHome(t)
	cfg.Cloud = config.CloudSection{Enabled: true, ClaimCode: "ABCD1234", Slug: "my-library", RelayWS: "wss://relay.example/ws"}
	k1, err := relayKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := relayKey()
	if err != nil || k1 != k2 || len(k1) != 64 {
		t.Fatalf("the key: %q then %q (%v)", k1, k2, err)
	}
	if info, err := os.Stat(RelayKeyPath()); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("the key's mode: %v", info.Mode().Perm())
	}
	m := envMap(relayEnv(cfg, k1))
	if m["MAKTABA_LOCAL_API"] != "http://127.0.0.1:8082" || m["MAKTABA_CRED_KEY_HEX"] != k1 ||
		m["MAKTABA_CRED_FILE"] != RelayCredPath() || m["MAKTABA_RELAY_WS"] != "wss://relay.example/ws" ||
		m["MAKTABA_CLAIM_CODE"] != "ABCD1234" || m["MAKTABA_SERVER_SLUG"] != "my-library" {
		t.Fatalf("the first run's environment: %v", m)
	}
	if err := os.WriteFile(RelayCredPath(), []byte("sealed"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, kv := range relayEnv(cfg, k1) {
		if strings.HasPrefix(kv, "MAKTABA_CLAIM_CODE=") {
			t.Fatal("the claim code goes to a run that has its credentials")
		}
	}
	// a key that is not one is an error, never replaced
	if err := os.WriteFile(RelayKeyPath(), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := relayKey(); err == nil {
		t.Fatal("a broken key was taken")
	}
}
