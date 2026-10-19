// Package slot is a Maktaba TV slot (Epic 33 story 33.1, design §2): one of up
// to three processes per streaming host (blue, green, amber) that run the
// channel engine behind the front door. The front door owns every client
// connection and routes each channel to the slot whose role is live; the hot
// standby runs the same release and keeps the watched channels warm so a live
// crash fails over at once; a third instance stages a candidate release (story
// 33.5). Roles rotate between the instances: the name carries no meaning.
//
// A slot listens on loopback only (8791/8792/8793; admin 9111/9112/9113),
// writes its HLS under <cache>/tv/<slot>/, beats tv_slots every 5 s, and runs a
// station only for a channel placed on its host while its role allows it.
package slot

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Instance names.
const (
	Blue  = "blue"
	Green = "green"
	Amber = "amber"
)

// Names is every slot instance, in order.
var Names = []string{Blue, Green, Amber}

// Roles (tv_slots.role).
const (
	RoleIdle     = "idle"
	RoleLive     = "live"
	RoleStandby  = "standby"
	RoleStaging  = "staging"
	RolePrevious = "previous"
	RoleDraining = "draining"
)

// Valid reports whether name is a slot instance.
func Valid(name string) bool {
	for _, n := range Names {
		if n == name {
			return true
		}
	}
	return false
}

func index(name string) int {
	for i, n := range Names {
		if n == name {
			return i
		}
	}
	return -1
}

// DefaultAddr is an instance's loopback public address (8791, 8792, 8793).
func DefaultAddr(name string) string {
	return "127.0.0.1:" + strconv.Itoa(8791+index(name))
}

// DefaultAdminAddr is an instance's loopback admin address (9111, 9112, 9113):
// /healthz and /readyz for the process watchdog.
func DefaultAdminAddr(name string) string {
	return "127.0.0.1:" + strconv.Itoa(9111+index(name))
}

// EnvSlots is how the supervisor tells the front door where its slots listen:
// "blue=127.0.0.1:8791,green=127.0.0.1:8792,amber=127.0.0.1:8793". Unset: the
// streaming process runs the channel engine itself (no slots).
const EnvSlots = "MAKTABA_TV_SLOTS"

// ParseAddrs reads EnvSlots' value: instance -> host:port.
func ParseAddrs(v string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(v, ",") {
		name, addr, ok := strings.Cut(strings.TrimSpace(part), "=")
		name = strings.TrimSpace(name)
		if !ok || !Valid(name) || strings.TrimSpace(addr) == "" {
			continue
		}
		out[name] = strings.TrimSpace(addr)
	}
	return out
}

// FormatAddrs is the inverse of ParseAddrs (in instance order).
func FormatAddrs(m map[string]string) string {
	var parts []string
	for _, n := range Names {
		if a, ok := m[n]; ok {
			parts = append(parts, n+"="+a)
		}
	}
	return strings.Join(parts, ",")
}

// Budget is what the front door lets a slot encode (design §14: live first, then
// the standby's warm set, then staging). Zero fields: the host's settings.
type Budget struct {
	MaxActive int `json:"max_active,omitempty"`
	NVENCMax  int `json:"nvenc_max,omitempty"`
	VTMax     int `json:"vt_max,omitempty"`
}

// HoldTTL: a warm hold the front door stopped refreshing ends after this (then
// the warm grace applies), so a slot outlives a front-door restart warm (AC7).
const HoldTTL = 30 * time.Second

// State is what a slot knows of itself: its role (from the front door, or
// tv_slots at each beat), its release binding, its budget and warm holds.
type State struct {
	Name    string
	Release string // the release this process was started for (MAKTABA_TV_RELEASE)

	mu       sync.Mutex
	role     string
	epoch    int64
	mismatch bool
	budget   Budget
	holds    map[uuid.UUID]time.Time
	draining bool
}

// NewState is a slot that has not heard its role yet (idle: no station).
func NewState(name, release string) *State {
	return &State{Name: name, Release: release, role: RoleIdle, holds: map[uuid.UUID]time.Time{}}
}

// Role is the slot's role and the epoch it was given under.
func (s *State) Role() (string, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.role, s.epoch
}

// SetRole adopts a role given under epoch (an older epoch is ignored).
func (s *State) SetRole(role string, epoch int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch < s.epoch || role == "" {
		return false
	}
	changed := role != s.role
	s.role, s.epoch = role, epoch
	return changed
}

// SetRelease checks the release tv_slots binds this slot to against the one it
// was started with (story 33.2 fills them; both empty until then).
func (s *State) SetRelease(bound string) {
	s.mu.Lock()
	s.mismatch = bound != "" && s.Release != "" && bound != s.Release
	s.mu.Unlock()
}

// ReleaseID is the release the slot airs: the one it was started for, or the
// one it was bound to later (BindRelease); "" while unbound (it then airs the
// live generation).
func (s *State) ReleaseID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Release
}

// BindRelease records the release an unbound slot was bound to (story 33.7:
// before a swap the live slot and the standby are pinned to the live release,
// so the swap's generation flip does not move what they air). A slot already
// bound keeps its release (false).
func (s *State) BindRelease(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Release != "" || id == "" {
		return s.Release == id
	}
	s.Release = id
	return true
}

// Mismatch reports a release mismatch (the slot runs no station).
func (s *State) Mismatch() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mismatch
}

// Live reports whether the front door routes viewers to this slot.
func (s *State) Live() bool {
	r, _ := s.Role()
	return r == RoleLive
}

// MayRun reports whether this slot may run stations now.
func (s *State) MayRun() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.mismatch:
		return false, "release_mismatch"
	case s.draining:
		return false, "draining"
	}
	switch s.role {
	case RoleLive, RoleStandby, RoleStaging, RolePrevious:
		return true, ""
	}
	return false, "role " + s.role
}

// Budget is the slot's encoder budget.
func (s *State) Budget() Budget {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.budget
}

// SetBudget sets the encoder budget.
func (s *State) SetBudget(b Budget) {
	s.mu.Lock()
	s.budget = b
	s.mu.Unlock()
}

// SetHolds replaces the warm set (now: when they were asked for).
func (s *State) SetHolds(ids []uuid.UUID, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holds = map[uuid.UUID]time.Time{}
	for _, id := range ids {
		s.holds[id] = now
	}
}

// Holds is the warm set still within its TTL.
func (s *State) Holds(now time.Time) []uuid.UUID {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []uuid.UUID
	for id, at := range s.holds {
		if now.Sub(at) <= HoldTTL {
			out = append(out, id)
		} else {
			delete(s.holds, id)
		}
	}
	return out
}

// Held reports whether a channel is in the warm set.
func (s *State) Held(id uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.holds[id]
	return ok
}

// Drain stops new stations (the slot is being retired).
func (s *State) Drain() {
	s.mu.Lock()
	s.draining = true
	s.mu.Unlock()
}

// Loopback reports whether a request's remote address is this machine.
func Loopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
