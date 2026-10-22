package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
)

// TestAForcedHomeIsNeverANode: a home whose server.toml says role = "home",
// beside a joined node.json of another install in the Windows roaming profile
// (the node config's fallback). Without a word from its supervisor, streaming
// would read that file and stream as the other install's node; with the
// variables a forced home's supervisor gives every role (MAKTABA_CLUSTER_ROLE
// and MAKTABA_TV_HOST_ROLE home, MAKTABA_NODE_CONFIG at a file never written)
// it is the home.
func TestAForcedHomeIsNeverANode(t *testing.T) {
	home := t.TempDir()
	roaming := filepath.Join(home, "AppData", "Roaming")
	b, _ := json.Marshal(map[string]string{"node_id": "6f1c9a2e-1111-4000-8000-000000000001",
		"database_url": "postgresql://node_6f1c9a2e@home/maktaba"})
	if err := os.MkdirAll(filepath.Join(roaming, "Maktaba"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roaming, "Maktaba", "node.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"APPDATA": roaming, "LOCALAPPDATA": filepath.Join(home, "AppData", "Local")}
	getenv := func(k string) string { return env[k] }

	// the gap: nothing said, the roaming node config makes it a node
	if _, ok, _ := NodeMode(getenv, "windows", home, os.ReadFile); !ok {
		t.Fatal("the roaming fallback is not read: the test proves nothing")
	}
	// a forced home's supervisor's word
	env["MAKTABA_CLUSTER_ROLE"], env["MAKTABA_TV_HOST_ROLE"] = "home", "home"
	env["MAKTABA_NODE_CONFIG"] = filepath.Join(home, "state", "home-has-no-node-config.json")
	if ns, ok, _ := NodeMode(getenv, "windows", home, os.ReadFile); ok {
		t.Errorf("a forced home streams as a node: %+v", ns)
	}
	if role, id, _ := channel.DetectRole(getenv, "windows", home, os.ReadFile); role != channel.RoleHome || id != "" {
		t.Errorf("DetectRole = %s %q, want the home", role, id)
	}
	// each word holds on its own
	for _, k := range []string{"MAKTABA_CLUSTER_ROLE", "MAKTABA_TV_HOST_ROLE", "MAKTABA_NODE_CONFIG"} {
		one := map[string]string{"APPDATA": env["APPDATA"], "LOCALAPPDATA": env["LOCALAPPDATA"], k: env[k]}
		if _, ok, _ := NodeMode(func(k string) string { return one[k] }, "windows", home, os.ReadFile); ok {
			t.Errorf("%s alone: still a node", k)
		}
	}
}
