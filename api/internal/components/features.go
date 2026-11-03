package components

// Features and the capabilities answer (story 51.09). A feature is one of the catalog's
// fixed user-facing words (live_tv, transcripts, …). It is on when every component that
// lists it is on, except that the members of a conflicts pair that both list it
// (livetv.slots, livetv.inprocess) count as one: either on is enough. A feature no
// component lists is always on.
//
// A component is on when its desired state says so (runner.Gates: want enabled, no
// hold, its requirements on) and the licence has its flag. Only the desired state and
// the licence are read: a crash or a restart never turns a feature off.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store/pqlisten"
)

// Feature answers.
const (
	FeatureOn  = "on"
	FeatureOff = "off"
)

// Reasons a feature is off, in the admin block. switched_off: a component it needs is
// not enabled. The others are the status reason codes of the same names.
const (
	ReasonSwitchedOff    = "switched_off"
	ReasonHeld           = "held"
	ReasonDependencyOff  = "dependency_off"
	ReasonLicenceMissing = "licence_missing"
)

// EnvCapabilitiesGate is `[components] capabilities_gate` as the supervisor passes it:
// "off" answers every feature on and turns the component_off check into a no-op.
const EnvCapabilitiesGate = "MAKTABA_COMPONENTS_CAPABILITIES_GATE"

// CapabilitiesGateOff reports whether the environment turns the gate off.
func CapabilitiesGateOff() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(EnvCapabilitiesGate)), "off")
}

// FeatureOffBy is why a feature is off: the components that turn it off and the reason
// of the first.
type FeatureOffBy struct {
	Components []string `json:"components"`
	Reason     string   `json:"reason"`
}

// Resolved is every feature's answer for one desired state and licence.
type Resolved struct {
	Features map[string]bool         // feature -> on
	Off      map[string]FeatureOffBy // the features that are off
	// Components are the components that are off, with their reason.
	Components map[string]string
}

// componentReason is why component id is off ("" when on).
func componentReason(c *components.Component, g runner.Gate, licensed func(string) bool) string {
	if !g.On {
		switch g.Reason {
		case "held":
			return ReasonHeld
		case "dependency_off":
			return ReasonDependencyOff
		}
		return ReasonSwitchedOff
	}
	if c.Licence != "" && licensed != nil && !licensed(c.Licence) {
		return ReasonLicenceMissing
	}
	return ""
}

// featureGroups are the groups of components a feature needs: each group is met when
// one of its members is on. A component is its own group, unless it conflicts with
// another component that lists the same feature: then they share one.
func featureGroups(cat *components.Catalog, feature string) [][]string {
	var listed []*components.Component
	for _, c := range cat.Components() {
		if slices.Contains(c.Features, feature) {
			listed = append(listed, c)
		}
	}
	var groups [][]string
	in := map[string]int{}
	for _, c := range listed {
		g, ok := -1, false
		for _, x := range c.Conflicts {
			if i, seen := in[x]; seen {
				g, ok = i, true
				break
			}
		}
		if !ok {
			groups = append(groups, nil)
			g = len(groups) - 1
		}
		groups[g] = append(groups[g], c.ID)
		in[c.ID] = g
	}
	return groups
}

// Resolve answers every catalog feature for desired (nil: nothing read, all on) and the
// licence (nil: every flag). allOn is the kill switch: every feature and component on.
func Resolve(cat *components.Catalog, desired map[string]store.Desired, licensed func(string) bool, allOn bool) Resolved {
	out := Resolved{Features: map[string]bool{}, Off: map[string]FeatureOffBy{}, Components: map[string]string{}}
	if !allOn {
		for id, g := range runner.Gates(cat, desired, false) {
			c, _ := cat.Get(id)
			if r := componentReason(c, g, licensed); r != "" {
				out.Components[id] = r
			}
		}
	}
	for _, f := range cat.Features {
		on := true
		var by FeatureOffBy
		for _, group := range featureGroups(cat, f) {
			if slices.ContainsFunc(group, func(id string) bool { return out.Components[id] == "" }) {
				continue
			}
			on = false
			if by.Reason == "" {
				by.Reason = out.Components[group[0]]
			}
			by.Components = append(by.Components, group...)
		}
		out.Features[f] = on
		if !on {
			out.Off[f] = by
		}
	}
	return out
}

// Snapshot is one capabilities answer, built when the desired state or the licence
// changes and read by every request with no query. It is read-only.
type Snapshot struct {
	etag   string
	viewer []byte
	admin  []byte
	// off: the components that are off -> the feature they serve ("" when none)
	off map[string]string
}

// ETag is c-<revision>-<licence>: the components' revision (component_meta, moved by
// every desired-state write) and a number of the licence flags the catalog names.
func (s *Snapshot) ETag() string { return s.etag }

// Body is the JSON answer: a viewer's (the features only) or an admin's.
func (s *Snapshot) Body(admin bool) []byte {
	if admin {
		return s.admin
	}
	return s.viewer
}

// ComponentOff reports whether component id is off, and the feature it serves.
func (s *Snapshot) ComponentOff(id string) (feature string, off bool) {
	feature, off = s.off[id]
	return feature, off
}

// AnyOff reports whether any component is off (the component_off check's fast path).
func (s *Snapshot) AnyOff() bool { return len(s.off) > 0 }

func buildSnapshot(cat *components.Catalog, r Resolved, etag string) *Snapshot {
	var b bytes.Buffer
	b.WriteString(`{"etag":`)
	writeJSON(&b, etag)
	b.WriteString(`,"features":{`)
	for i, f := range cat.Features {
		if i > 0 {
			b.WriteByte(',')
		}
		writeJSON(&b, f)
		b.WriteByte(':')
		if r.Features[f] {
			writeJSON(&b, FeatureOn)
		} else {
			writeJSON(&b, FeatureOff)
		}
	}
	b.WriteByte('}')
	viewer := append(slices.Clone(b.Bytes()), '}')
	b.WriteString(`,"admin":{"manage_components":true,"off":{`)
	n := 0
	for _, f := range cat.Features {
		by, ok := r.Off[f]
		if !ok {
			continue
		}
		if n > 0 {
			b.WriteByte(',')
		}
		n++
		writeJSON(&b, f)
		b.WriteByte(':')
		writeJSON(&b, by)
	}
	b.WriteString(`}}}`)
	off := make(map[string]string, len(r.Components))
	for id := range r.Components {
		c, _ := cat.Get(id)
		f := ""
		if len(c.Features) > 0 {
			f = c.Features[0]
		}
		off[id] = f
	}
	return &Snapshot{etag: etag, viewer: viewer, admin: b.Bytes(), off: off}
}

func writeJSON(b *bytes.Buffer, v any) {
	raw, _ := json.Marshal(v)
	b.Write(raw)
}

// CoalesceWindow is how long a change waits for the next before it is pushed: a bundle
// switch is one push.
const CoalesceWindow = time.Second

// LicencePoll is how often the licence is looked at again (it lapses with time).
const LicencePoll = 30 * time.Second

// Capabilities holds the home's capabilities snapshot (story 51.09), kept in step with
// the desired state (a store.Watcher) and the licence. Until Run reads the database
// every feature is on, as before the epic.
type Capabilities struct {
	cat   *components.Catalog
	log   *slog.Logger
	allOn bool

	snap atomic.Pointer[Snapshot]

	mu       sync.Mutex
	licensed func(string) bool
	desired  map[string]store.Desired
	revision int64
	changed  chan struct{}
}

// CapabilitiesOptions make a Capabilities.
type CapabilitiesOptions struct {
	Catalog *components.Catalog // nil: the embedded one
	Logger  *slog.Logger
	// AllOn is the kill switch: [components] capabilities_gate = off, or reconcile = off
	// (the runners act on nothing, so everything runs).
	AllOn bool
}

// NewCapabilities is a holder whose snapshot answers every feature on.
func NewCapabilities(o CapabilitiesOptions) *Capabilities {
	if o.Catalog == nil {
		o.Catalog = components.Default()
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	c := &Capabilities{cat: o.Catalog, log: o.Logger, allOn: o.AllOn, changed: make(chan struct{}, 1)}
	c.mu.Lock()
	c.rebuild()
	c.mu.Unlock()
	return c
}

// Snapshot is the current answer (lock-free). A nil holder answers everything on.
func (c *Capabilities) Snapshot() *Snapshot {
	if c == nil {
		return allOnSnapshot
	}
	return c.snap.Load()
}

var allOnSnapshot = buildSnapshot(components.Default(), Resolve(components.Default(), nil, nil, true), "c-0-0")

// SetLicensed sets the licence check (nil: every flag) and rebuilds.
func (c *Capabilities) SetLicensed(fn func(flag string) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.licensed = fn
	c.rebuild()
}

// Apply takes a new desired state (what the watcher hands Run; tests call it).
func (c *Capabilities) Apply(desired map[string]store.Desired, revision int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.desired, c.revision = desired, revision
	c.rebuild()
}

// Recheck builds the snapshot again (the licence may have changed).
func (c *Capabilities) Recheck() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rebuild()
}

// licenceMask is a number for which of the catalog's licence flags the licence has: the
// same on every replica for the same licence.
func (c *Capabilities) licenceMask() int64 {
	if c.allOn {
		return 0
	}
	var flags []string
	for _, comp := range c.cat.Components() {
		if comp.Licence != "" && !slices.Contains(flags, comp.Licence) {
			flags = append(flags, comp.Licence)
		}
	}
	slices.Sort(flags)
	var m int64
	for i, f := range flags {
		if c.licensed == nil || c.licensed(f) {
			m |= 1 << i
		}
	}
	return m
}

// rebuild makes the snapshot (c.mu held) and signals a changed ETag.
func (c *Capabilities) rebuild() {
	etag := "c-" + strconv.FormatInt(c.revision, 10) + "-" + strconv.FormatInt(c.licenceMask(), 10)
	old := c.snap.Load()
	c.snap.Store(buildSnapshot(c.cat, Resolve(c.cat, c.desired, c.licensed, c.allOn), etag))
	if old != nil && old.etag == etag {
		return
	}
	select {
	case c.changed <- struct{}{}:
	default:
	}
}

// Publisher pushes capabilities_changed with the new ETag.
type Publisher func(ctx context.Context, etag string) error

// Run keeps the snapshot in step until ctx ends: the desired state from src (listener
// nil: the 30 s revision poll only), the licence every LicencePoll, and publish (nil:
// none) after each change, at most once per CoalesceWindow.
func (c *Capabilities) Run(ctx context.Context, src store.Source, listener store.Listener, publish Publisher) {
	if listener != nil {
		defer func() { _ = listener.Close() }()
	}
	if src != nil {
		w := &store.Watcher{Source: src, Machine: store.Home, Listener: listener, OnError: func(err error) {
			c.log.Warn("capabilities: reading the desired state failed", "err", err.Error(), "event", "capabilities_read_failed")
		}}
		go func() { _ = w.Run(ctx, c.Apply) }()
	}
	lic := time.NewTicker(LicencePoll)
	defer lic.Stop()
	sent := ""
	var due <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-lic.C:
			c.Recheck()
		case <-c.changed:
			if due == nil {
				due = time.After(CoalesceWindow)
			}
		case <-due:
			due = nil
			etag := c.Snapshot().ETag()
			if publish == nil || etag == sent {
				continue
			}
			if err := publish(ctx, etag); err != nil {
				if ctx.Err() == nil {
					c.log.Warn("capabilities: the push failed", "err", err.Error(), "event", "capabilities_publish_failed")
				}
				continue
			}
			sent = etag
		}
	}
}

// StartCapabilities runs c against db for the rest of the process (a LISTEN on dsn when
// it is Postgres). It never blocks.
func StartCapabilities(c *Capabilities, db store.Source, isSQLite bool, dsn string, publish Publisher, logger *slog.Logger) {
	if c == nil || db == nil {
		return
	}
	var l store.Listener
	if !isSQLite && dsn != "" {
		pl, err := pqlisten.New(dsn, nil)
		if err != nil {
			logger.Warn("capabilities: no change listener; polling the desired state every 30 s",
				"err", err.Error(), "event", "capabilities_listen_failed")
		} else {
			l = pl
		}
	}
	go c.Run(context.Background(), db, l, publish)
}
