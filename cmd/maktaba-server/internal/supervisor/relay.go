package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// RoleRelay is the cloud relay agent (maktaba-cloudlink, Epic 25): it keeps the home's
// tunnel to the cloud up, so the cloud's edge reaches this home's web port on loopback
// (relayLocal; loopback is the only peer the web port and the API take X-Maktaba-Via
// from). It runs on the home only, while [cloud]
// enabled and the home has the agent's credentials, or a claim code to get them: a
// switch of the home moves them, and the agent with them (specs/epics/37-node-replica).
const RoleRelay Role = "relay"

// The relay agent's files in the keys directory: its credentials, sealed at rest
// (AES-256) with its key, which this supervisor makes once.
const (
	RelayCredFile = "cloudlink.cred"
	RelayKeyFile  = "cloudlink.key"
	// RelayIdentityFile is what the agent writes beside its credentials once
	// claimed (cloud/internal/cloudlink IdentityFile): the home's server id in
	// the cloud and the cloud's pinned assertion key, which the API reads to
	// verify linked-account sign-ins (Epic 42 story 42.7).
	RelayIdentityFile = "cloudlink.identity.json"
)

// RelayAdminDefault is the agent's loopback status address ([cloud] admin_addr).
var RelayAdminDefault = ports.RelayAdmin.Addr()

// RelayCredPath and RelayKeyPath are the agent's files.
func RelayCredPath() string { return filepath.Join(config.KeysDir(), RelayCredFile) }
func RelayKeyPath() string  { return filepath.Join(config.KeysDir(), RelayKeyFile) }

// RelayIdentityPath is the agent's identity file.
func RelayIdentityPath() string { return filepath.Join(config.KeysDir(), RelayIdentityFile) }

// RelayWanted reports whether this machine runs the relay agent: the home (never a
// machine joined to one, nor one whose node config cannot be read), with [cloud] enabled
// and the agent's credentials, or a claim code and slug to get them.
func RelayWanted(cfg config.Config) bool { return relayWanted(cfg, nodeConfig()) }

// relayWanted is RelayWanted with the node config n read.
func relayWanted(cfg config.Config, n config.NodeConfig) bool {
	if !cfg.Cloud.Enabled || ((n.Joined() || n.Err != nil) && !forcedHome(cfg)) {
		return false
	}
	if _, err := os.Stat(RelayCredPath()); err == nil {
		return true
	}
	return strings.TrimSpace(cfg.Cloud.ClaimCode) != "" && strings.TrimSpace(cfg.Cloud.Slug) != ""
}

// RelayAdminAddr is the agent's loopback status address.
func RelayAdminAddr(cfg config.Config) string {
	return orDefault(cfg.Cloud.AdminAddr, RelayAdminDefault)
}

// RelayStatusURL is the agent's status (GET: its link state; 503 while the tunnel is
// down).
func RelayStatusURL(cfg config.Config) string {
	return "http://" + RelayAdminAddr(cfg) + "/admin/cloud-link"
}

// relayKey reads the agent's credential key (64 hex characters), made once.
func relayKey() (string, error) {
	path := RelayKeyPath()
	if b, err := os.ReadFile(path); err == nil {
		k := strings.TrimSpace(string(b))
		if raw, err := hex.DecodeString(k); err == nil && len(raw) == 32 {
			return k, nil
		}
		return "", errors.New("supervisor: the relay agent's key (" + path + ") is not 32 bytes of hex")
	} else if !os.IsNotExist(err) {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	k := hex.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(k+"\n"), 0o600); err != nil {
		return "", err
	}
	return k, os.Rename(tmp, path)
}

// relayLocal is where the agent sends what reaches the home through the cloud: the web
// port on loopback, which serves the web UI, /api (to the API) and /stream/ (the
// streaming service, behind its sign-in gate), so the relay subdomain is the whole home;
// the API port when this machine runs no web UI.
func relayLocal(cfg config.Config) string {
	if strings.TrimSpace(cfg.Server.WebListen) != "" {
		return "http://" + LocalAddr(cfg.Server.WebListen, ports.Web.PortString())
	}
	return "http://" + LocalAddr(cfg.Server.Listen, ports.APIPublic.PortString())
}

// relayEnv is the agent's environment: this machine's web port on loopback, its
// credentials and key, the cloud's addresses when set, and the claim for its first run
// only.
func relayEnv(cfg config.Config, key string) []string {
	env := []string{
		"MAKTABA_LOCAL_API=" + relayLocal(cfg),
		"MAKTABA_CRED_FILE=" + RelayCredPath(),
		"MAKTABA_CRED_KEY_HEX=" + key,
		"MAKTABA_ADMIN_ADDR=" + RelayAdminAddr(cfg),
	}
	if v := strings.TrimSpace(cfg.Cloud.API); v != "" {
		env = append(env, "MAKTABA_CLOUD_API="+v)
	}
	if v := strings.TrimSpace(cfg.Cloud.RelayWS); v != "" {
		env = append(env, "MAKTABA_RELAY_WS="+v)
	}
	if _, err := os.Stat(RelayCredPath()); err != nil {
		env = append(env, "MAKTABA_CLAIM_CODE="+strings.TrimSpace(cfg.Cloud.ClaimCode),
			"MAKTABA_SERVER_SLUG="+strings.TrimSpace(cfg.Cloud.Slug),
			"MAKTABA_SERVER_NAME="+orDefault(cfg.Cloud.Name, "maktaba"))
	}
	return env
}

// BuildRelay constructs the relay agent's process (RelayWanted). Its check is that its
// status address answers at all: a tunnel that is down (503) is the cloud's state, not
// a hang of the agent, which reconnects on its own.
func BuildRelay(opts Options) (Process, error) {
	bin, err := opts.resolve("maktaba-cloudlink", "MAKTABA_CLOUDLINK_BIN")
	if err != nil {
		return Process{}, err
	}
	key, err := relayKey()
	if err != nil {
		return Process{}, err
	}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), relayEnv(opts.Config, key)...)
	wire(cmd)
	addr := RelayAdminAddr(opts.Config)
	return Process{
		Role:  RoleRelay,
		Cmd:   cmd,
		Probe: "http://" + addr + "/admin/cloud-link",
		Binds: []ports.Bind{{Addr: addr, What: "status port"}},
		Check: func(ctx context.Context) error {
			d := net.Dialer{Timeout: 2 * time.Second}
			c, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				return err
			}
			return c.Close()
		},
	}, nil
}
