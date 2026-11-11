package supervisor

import (
	"encoding/json"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// Story 59.13: setup's share step reaches the pipeline as MAKTABA_HOME_SHARES, and the
// NAS platform as MAKTABA_PLATFORM (the install file's kept); neither reaches the api.
func TestHomeSharesEnv(t *testing.T) {
	t.Setenv("MAKTABA_HOME_SHARES", "")
	t.Setenv("MAKTABA_PLATFORM", "")
	cfg := config.Defaults()
	if v := envMap(childEnvOK(RolePipeline, cfg))["MAKTABA_HOME_SHARES"]; v != "" {
		t.Fatalf("no share step, no env: %q", v)
	}
	cfg.Media.Shares = map[string]string{"/mnt/tank/media": "media", "/mnt/tank/home": ""}
	cfg.Media.ShareServer = "truenas"
	cfg.Media.ShareAliases = []string{"truenas.local", "10.0.0.5"}
	var got struct {
		Server  string            `json:"server"`
		Aliases []string          `json:"aliases"`
		Roots   map[string]string `json:"roots"`
	}
	if err := json.Unmarshal([]byte(envMap(childEnvOK(RolePipeline, cfg))["MAKTABA_HOME_SHARES"]), &got); err != nil {
		t.Fatal(err)
	}
	if got.Server != "truenas" || len(got.Aliases) != 2 || got.Roots["/mnt/tank/media"] != "media" || got.Roots["/mnt/tank/home"] != "" {
		t.Fatalf("env %+v", got)
	}
	if v := envMap(childEnvOK(RoleAPI, cfg))["MAKTABA_HOME_SHARES"]; v != "" {
		t.Fatalf("the api does not read it: %q", v)
	}
	t.Setenv("MAKTABA_PLATFORM", "dsm")
	if _, ok := envMap(childEnvOK(RolePipeline, cfg))["MAKTABA_PLATFORM"]; ok {
		t.Fatal("the install file's MAKTABA_PLATFORM is inherited, not set again")
	}
}
