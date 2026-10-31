package supervisor

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
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

// SlotAddr is an instance's loopback public address (ports.TVSlotPublic:
// 8791/8792/8793).
func SlotAddr(name string) string { return ports.TVSlotPublic.AddrAt(slotIndex(name)) }

// SlotAdminAddr is an instance's loopback admin address (ports.TVSlotAdmin:
// 9111/9112/9113).
func SlotAdminAddr(name string) string { return ports.TVSlotAdmin.AddrAt(slotIndex(name)) }

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
		Held:  heldQuery(opts.Config.Database.URL),
		Binds: []ports.Bind{{Addr: SlotAddr(name), What: "channel port"}, {Addr: SlotAdminAddr(name), What: "admin port"}}}, nil
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
	base := "http://" + LocalAddr(streamAddr, ports.StreamingPublic.PortString())
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
// is the release's (ResolveReleaseBinary), and gets MAKTABA_TV_RELEASE. A binding with
// no version (a release that names no streaming binary) runs what an unbound slot runs:
// the binary the live TV is held on while it is held (held.go), else this release's.
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
	if b == nil || b.ReleaseID == "" || unversioned(b) {
		if base != "" && cmd.Path != base { // unbound again: the binary built with
			cmd.Path = base
			if len(cmd.Args) > 0 {
				cmd.Args[0] = base
			}
		}
		if b == nil || b.ReleaseID == "" {
			return nil
		}
	} else {
		rb, err := resolveReleaseBinary(releaseBinRoot(base), b.Version, b.SHA256, version.Version, base, kept)
		if err != nil {
			return fmt.Errorf("tv_slot_refused: %w", err)
		}
		cmd.Path = rb.Path
		if len(cmd.Args) > 0 {
			cmd.Args[0] = rb.Path
		}
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

// unversioned: a binding to a release that names no streaming binary.
func unversioned(b *SlotBinding) bool { return b != nil && strings.TrimSpace(b.Version) == "" }

// ErrBindingBinary: the release a slot is asked to run names a streaming binary this
// install has nowhere (bin/releases/<v>, nor a kept copy), or not with its checksum.
var ErrBindingBinary = errors.New("the TV release's streaming binary is not on this machine")

// CheckBinding reports, before a slot instance is bound to b and started, whether the
// binary b names can be run: a staging slot for a release held on an old version whose
// binary is gone is refused at once, with the reason, instead of starting and failing
// (or running other software than the release's).
func (s *Supervisor) CheckBinding(instance string, b *SlotBinding) error {
	if b == nil || b.ReleaseID == "" || unversioned(b) {
		return nil
	}
	s.mu.Lock()
	base := ""
	found := false
	for _, c := range s.children {
		if c.proc.Role == RoleTVSlot && c.proc.Instance == instance {
			base, found = c.basePath, true
		}
	}
	kept := s.keptStreaming
	s.mu.Unlock()
	if !found {
		return fmt.Errorf("no %s process %q", RoleTVSlot, instance)
	}
	if _, err := resolveReleaseBinary(releaseBinRoot(base), b.Version, b.SHA256, version.Version, base, kept); err != nil {
		msg := fmt.Sprintf("tv-slot/%s cannot run TV release %s: its streaming binary %s is not on this machine (%v)",
			instance, b.ReleaseID, b.Version, err)
		s.errorf("%s", msg)
		s.emit(Event{Role: RoleTVSlot, Kind: EventTVHeldMissing, Message: msg})
		return fmt.Errorf("%w: streaming %s for release %s (%v)", ErrBindingBinary, b.Version, b.ReleaseID, err)
	}
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
	_, err := s.bind(instance, b)
	return err
}

// bind is Bind that also says whether the instance runs now on another binding:
// its process keeps the old release until it starts again.
func (s *Supervisor) bind(instance string, b *SlotBinding) (stale bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.children {
		if c.proc.Role == RoleTVSlot && c.proc.Instance == instance {
			stale = c.cmd != nil && !sameBinding(c.binding, b)
			c.binding = b
			return stale, nil
		}
	}
	return false, fmt.Errorf("no %s process %q", RoleTVSlot, instance)
}

// sameBinding: a and b run the same release (nil and an empty binding are both unbound).
func sameBinding(a, b *SlotBinding) bool {
	var x, y SlotBinding
	if a != nil {
		x = *a
	}
	if b != nil {
		y = *b
	}
	return x == y
}
