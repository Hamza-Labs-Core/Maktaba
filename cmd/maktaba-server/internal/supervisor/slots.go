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
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
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
	if NodeJoined(cfg) {
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
	bin, err := opts.resolve("maktaba-streaming", "MAKTABA_STREAMING_BIN")
	if err != nil {
		return Process{}, err
	}
	slotEnv, err := ChildEnv(RoleTVSlot, opts.Config)
	if err != nil {
		return Process{}, err
	}
	env := append(os.Environ(), slotEnv...)
	env = append(env, "MAKTABA_TV_SLOT="+name, "MAKTABA_HTTP_ADDR="+SlotAddr(name),
		"MAKTABA_ADMIN_ADDR="+SlotAdminAddr(name))
	cmd := exec.Command(bin, "slot", "--slot", name)
	cmd.Env = env
	wire(cmd)
	return Process{Role: RoleTVSlot, Instance: name, OnDemand: slotOnDemand[name], Cmd: cmd,
		Probe: "http://" + SlotAdminAddr(name) + "/healthz", Survives: true,
		Held: heldQuery(opts.Config.Database.URL)}, nil
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
			if err != nil {
				// the front door learns it at its next probe instead
				fmt.Fprintf(os.Stderr, "[supervisor] level=warn telling the front door %s ended: %v\n", p.Name(), err)
				return
			}
			_ = resp.Body.Close()
			if resp.StatusCode/100 != 2 {
				fmt.Fprintf(os.Stderr, "[supervisor] level=warn telling the front door %s ended: it answered %s\n", p.Name(), resp.Status)
			}
		}()
	}
}

// SlotBinding binds a slot instance to a TV release (story 33.2): the slot runs
// that release's streaming binary, bin/releases/<version>/, only when its SHA-256
// is the release's (ResolveReleaseBinary), and gets MAKTABA_TV_RELEASE.
type SlotBinding struct {
	ReleaseID string `json:"release_id"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
}

// applyBinding points a slot's command at its release's binary (at each start:
// a restart re-checks the file). No binding: the command as built.
func applyBinding(cmd *exec.Cmd, b *SlotBinding, base string) error {
	return applyBindingKept(cmd, b, base, nil)
}

// applyBindingKept is applyBinding that also finds the release's binary among kept(version)
// (WithKeptStreaming).
func applyBindingKept(cmd *exec.Cmd, b *SlotBinding, base string, kept func(string) []string) error {
	if b == nil || b.ReleaseID == "" {
		if base != "" && cmd.Path != base { // unbound again: the binary built with
			cmd.Path = base
			if len(cmd.Args) > 0 {
				cmd.Args[0] = base
			}
		}
		return nil
	}
	rb, err := resolveReleaseBinary(releaseBinRoot(base), b.Version, b.SHA256, version.Version, base, kept)
	if err != nil {
		return fmt.Errorf("tv_slot_refused: %w", err)
	}
	cmd.Path = rb.Path
	if len(cmd.Args) > 0 {
		cmd.Args[0] = rb.Path
	}
	env := make([]string, 0, len(cmd.Env)+1)
	for _, e := range cmd.Env {
		if !strings.HasPrefix(e, "MAKTABA_TV_RELEASE=") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "MAKTABA_TV_RELEASE="+b.ReleaseID)
	return nil
}

// WithKeptStreaming lets a slot bound to a release whose binary is not under
// bin/releases/<version> run it from where this install keeps it (kept: the paths of
// that release's streaming binary; a desktop app's copy kept before its update, another
// runtime directory). A TV held on the release before stays on it across restarts.
func WithKeptStreaming(kept func(version string) []string) Option {
	return func(s *Supervisor) { s.keptStreaming = kept }
}

// Bind sets (nil: clears) the TV release a slot instance runs from its next
// start on.
func (s *Supervisor) Bind(instance string, b *SlotBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.children {
		if c.proc.Role == RoleTVSlot && c.proc.Instance == instance {
			c.binding = b
			return nil
		}
	}
	return fmt.Errorf("no %s process %q", RoleTVSlot, instance)
}
