package supervisor

import (
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/clientip"
)

// Epic 59 story 59.08: [server] trusted_proxies reaches the api and streaming (and no
// other role) as MAKTABA_TRUSTED_PROXIES, each entry as a CIDR; a bad entry stops the role
// with the key named.
func TestTrustedProxiesEnv(t *testing.T) {
	t.Setenv("MAKTABA_ROLE", "home")
	cfg := config.Defaults()
	if m := envMap(childEnvOK(RoleAPI, cfg)); m[clientip.EnvTrustedProxies] != "" {
		t.Fatalf("empty list: %q", m[clientip.EnvTrustedProxies])
	}
	cfg.Server.TrustedProxies = []string{"192.168.1.5", " ", "10.0.0.0/8"}
	for _, role := range []Role{RoleAPI, RoleStreaming} {
		if got := envMap(childEnvOK(role, cfg))[clientip.EnvTrustedProxies]; got != "192.168.1.5/32,10.0.0.0/8" {
			t.Errorf("%s: %q", role, got)
		}
	}
	if _, ok := envMap(childEnvOK(RolePipeline, cfg))[clientip.EnvTrustedProxies]; ok {
		t.Error("the pipeline got the list")
	}
	cfg.Server.TrustedProxies = []string{"nas.local"}
	if _, err := ChildEnv(RoleAPI, cfg); err == nil || !strings.Contains(err.Error(), "server.trusted_proxies[0]") {
		t.Fatalf("bad entry: %v", err)
	}
}
