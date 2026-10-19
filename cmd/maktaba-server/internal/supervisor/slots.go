package supervisor

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// The tv-slot role (Epic 33 story 33.1): the channel engine runs in up to three
// slot processes per streaming host, `maktaba-streaming slot --slot
// blue|green|amber`, behind the streaming role, which becomes the front door
// (MAKTABA_TV_SLOTS). Two run in steady state — the live slot and the hot
// standby; the third (amber) only on request (staging, story 33.5), and the
// front door may stop the standby (rules.tv_staging.hot_standby off). Roles
// rotate between the instances: the supervisor only keeps the processes the
// front door wants running, restarted with backoff and probed like any role.

// RoleTVSlot is a TV slot instance.
const RoleTVSlot Role = "tv-slot"

// Slot instances.
var SlotNames = []string{"blue", "green", "amber"}

// slotOnDemand: instances not started at boot.
var slotOnDemand = map[string]bool{"amber": true}

func slotIndex(name string) int {
	for i, n := range SlotNames {
		if n == name {
			return i
		}
	}
	return -1
}

// ValidSlot reports a slot instance name.
func ValidSlot(name string) bool { return slotIndex(name) >= 0 }

// SlotAddr is an instance's loopback public address (8791/8792/8793).
func SlotAddr(name string) string { return "127.0.0.1:" + strconv.Itoa(8791+slotIndex(name)) }

// SlotAdminAddr is an instance's loopback admin address (9111/9112/9113).
func SlotAdminAddr(name string) string { return "127.0.0.1:" + strconv.Itoa(9111+slotIndex(name)) }

// SlotsEnv is the front door's MAKTABA_TV_SLOTS value.
func SlotsEnv() string {
	parts := make([]string, 0, len(SlotNames))
	for _, n := range SlotNames {
		parts = append(parts, n+"="+SlotAddr(n))
	}
	return strings.Join(parts, ",")
}

// SlotsEnabled reports whether this install runs TV slots: [tv] slots (default
// on), on a home (a joined node streams in one process, Epic 32.1) with a
// Postgres database (the slots' heartbeat and roles).
func SlotsEnabled(cfg config.Config) bool {
	if !cfg.TV.SlotsOn() {
		return false
	}
	if nodeConfig().Joined() {
		return false
	}
	u := strings.ToLower(strings.TrimSpace(cfg.Database.URL))
	return strings.HasPrefix(u, "postgres://") || strings.HasPrefix(u, "postgresql://")
}

// BuildSlot constructs a slot instance's process.
func BuildSlot(name string, opts Options) (Process, error) {
	if !ValidSlot(name) {
		return Process{}, fmt.Errorf("unknown TV slot %q (blue, green or amber)", name)
	}
	bin, err := resolveBinary("maktaba-streaming", "MAKTABA_STREAMING_BIN", opts.BinDir)
	if err != nil {
		return Process{}, err
	}
	env := append(os.Environ(), ChildEnv(RoleTVSlot, opts.Config)...)
	env = append(env, "MAKTABA_TV_SLOT="+name, "MAKTABA_HTTP_ADDR="+SlotAddr(name),
		"MAKTABA_ADMIN_ADDR="+SlotAdminAddr(name))
	cmd := exec.Command(bin, "slot", "--slot", name)
	cmd.Env = env
	wire(cmd)
	return Process{Role: RoleTVSlot, Instance: name, OnDemand: slotOnDemand[name], Cmd: cmd,
		Probe: "http://" + SlotAdminAddr(name) + "/healthz"}, nil
}

// BuildSlots is every slot instance.
func BuildSlots(opts Options) ([]Process, error) {
	var out []Process
	for _, n := range SlotNames {
		p, err := BuildSlot(n, opts)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// SlotExitReporter tells the front door at once that a slot process ended
// (faster than its next probe): POST <stream>/internal/tv/slot-exited/<name>.
func SlotExitReporter(streamAddr string) func(Process, ExitInfo) {
	base := "http://" + LocalAddr(streamAddr, "8081")
	client := &http.Client{Timeout: 2 * time.Second}
	return func(p Process, _ ExitInfo) {
		if p.Role != RoleTVSlot {
			return
		}
		go func() {
			resp, err := client.Post(base+"/internal/tv/slot-exited/"+p.Instance, "application/json", nil)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
	}
}
