package appruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// The runtime root's links and files besides the installed directories.
const (
	// CurrentLink names the runtime the service runs.
	CurrentLink = "current"
	// PreviousLink names the one that ran before it: what a rollback goes back
	// to, and what prune keeps.
	PreviousLink = "previous"
	// watermarkFn holds the newest version this runtime ever ran (it only rises).
	watermarkFn = "watermark"
	ownerFile   = "owner.json"
)

var (
	// ErrNotInstalled: no installed runtime directory of that name.
	ErrNotInstalled = errors.New("not_installed")
	// ErrDowngrade: a version older than the runtime's watermark.
	ErrDowngrade = errors.New("downgrade")
	// ErrService: stopping or starting the service failed (`current` is as it
	// was, or put back).
	ErrService = errors.New("service")
)

// Service is the unit that runs the runtime (the systemd user service).
type Service interface {
	// Active reports whether it runs now (an installed unit that is active or starting).
	Active(ctx context.Context) bool
	Stop(ctx context.Context) error
	Start(ctx context.Context) error
}

// Current is the directory name `current` points at ("" when there is none).
func (r Runtime) Current() (string, error) { return r.link(CurrentLink) }

// Previous is the directory that ran before current ("" when none is
// recorded, or it was removed since).
func (r Runtime) Previous() string {
	name, err := r.link(PreviousLink)
	if err != nil || name == "" {
		return ""
	}
	if _, err := readInstalled(filepath.Join(r.Root, name)); err != nil {
		return ""
	}
	return name
}

func (r Runtime) link(name string) (string, error) {
	t, err := os.Readlink(filepath.Join(r.Root, name))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return filepath.Base(t), err
}

// Activate points `current` at an installed directory with the service
// stopped: it stops a running service, moves the link, and starts the service
// again (a stopped one, paused by the user, stays stopped and runs the new
// runtime when resumed). A start that fails puts the old link back and starts
// that. svc may be nil (no service installed yet). The directory that ran
// before is recorded as `previous`, and the watermark rises to the version.
func (r Runtime) Activate(ctx context.Context, name string, svc Service, opt Options) (previous string, err error) {
	name = filepath.Base(name)
	if link := name; link == CurrentLink || link == PreviousLink { // the directory the link names, never the link
		if name, err = r.link(link); err != nil || name == "" {
			return "", fmt.Errorf("%w: no %s runtime is recorded", ErrNotInstalled, link)
		}
	}
	inst, err := readInstalled(filepath.Join(r.Root, name))
	if err != nil {
		return "", fmt.Errorf("%w: %s is not an installed runtime (%v)", ErrNotInstalled, filepath.Join(r.Root, name), err)
	}
	if inst.Platform != Platform() {
		return "", fmt.Errorf("%w: %s is for %s", ErrInvalid, name, inst.Platform)
	}
	unlock, err := r.lock(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	previous, err = r.Current()
	if err != nil {
		return "", err
	}
	if previous == name {
		return previous, nil
	}
	if err := r.checkVersion(inst.Version, opt); err != nil {
		return previous, err
	}
	running := svc != nil && svc.Active(ctx)
	if running {
		if err := svc.Stop(ctx); err != nil {
			return previous, fmt.Errorf("%w: stop the service: %v", ErrService, err)
		}
	}
	if err := r.setLink(CurrentLink, name); err != nil {
		if running {
			_ = svc.Start(ctx)
		}
		return previous, err
	}
	if running {
		if err := svc.Start(ctx); err != nil {
			if previous != "" {
				_ = r.setLink(CurrentLink, previous)
				_ = svc.Start(ctx)
			}
			return previous, fmt.Errorf("%w: start the service on %s (%s is back): %v", ErrService, name, orNone(previous), err)
		}
	}
	if previous != "" {
		if err := r.setLink(PreviousLink, previous); err != nil {
			return previous, err
		}
	}
	return previous, r.raiseWatermark(inst.Version)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// setLink moves the link name to target: a new link renamed over the old
// (atomic).
func (r Runtime) setLink(name, target string) error {
	tmp := filepath.Join(r.Root, name+".new")
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(r.Root, name)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	r.sync()
	return nil
}

// Watermark is the newest version this runtime ever ran ("" when none).
func (r Runtime) Watermark() string {
	b, err := os.ReadFile(filepath.Join(r.Root, watermarkFn))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// raiseWatermark sets the watermark to v when v is newer (it never falls).
func (r Runtime) raiseWatermark(v string) error {
	if w := r.Watermark(); w != "" && layout.CompareVersions(v, w) <= 0 {
		return nil
	}
	tmp := filepath.Join(r.Root, watermarkFn+".new")
	if err := os.WriteFile(tmp, []byte(v+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(r.Root, watermarkFn))
}

// checkVersion refuses a version older than the watermark (Epic 35's rule:
// never older than the newest this machine ran) unless opt allows it.
func (r Runtime) checkVersion(v string, opt Options) error {
	w := r.Watermark()
	if opt.AllowDowngrade || w == "" || layout.CompareVersions(v, w) >= 0 {
		return nil
	}
	return fmt.Errorf("%w: %s is older than %s, the newest version this machine has run "+
		"(an older Maktaba would run a database a newer one migrated; --allow-downgrade does it anyway)", ErrDowngrade, v, w)
}

// Dir is one installed runtime directory.
type Dir struct {
	Name string `json:"name"`
	Installed
}

// List is every installed runtime directory, newest install first.
func (r Runtime) List() ([]Dir, error) {
	ents, err := os.ReadDir(r.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Dir
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if inst, err := readInstalled(filepath.Join(r.Root, e.Name())); err == nil {
			out = append(out, Dir{Name: e.Name(), Installed: inst})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstalledAt.After(out[j].InstalledAt) })
	return out, nil
}

// Prune keeps current, the one that ran before it (`previous`) and the newest
// others up to keep in all, and removes the rest (and what an install cut
// short left). It returns what it removed.
func (r Runtime) Prune(ctx context.Context, keep int) ([]string, error) {
	if keep < 2 {
		keep = 2 // never fewer than the running one and the one before
	}
	unlock, err := r.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	r.removeStaging()
	cur, _ := r.Current()
	dirs, err := r.List()
	if err != nil {
		return nil, err
	}
	kept := map[string]bool{}
	for _, n := range []string{cur, r.Previous()} {
		if n != "" {
			kept[n] = true
		}
	}
	for _, d := range dirs { // newest install first
		if len(kept) >= keep {
			break
		}
		kept[d.Name] = true
	}
	var removed []string
	for _, d := range dirs {
		if kept[d.Name] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(r.Root, d.Name)); err != nil {
			return removed, err
		}
		removed = append(removed, d.Name)
	}
	return removed, nil
}

// Owners of a runtime: who updates it.
const (
	OwnerApp      = "app"      // the desktop shell, through its updater
	OwnerHeadless = "headless" // the machine itself, from Epic 35's verified releases
)

// Owner is owner.json.
type Owner struct {
	Owner string `json:"owner"`
}

// ReadOwner is the runtime's owner (OwnerApp when none was recorded).
func (r Runtime) ReadOwner() string {
	var o Owner
	if b, err := os.ReadFile(filepath.Join(r.Root, ownerFile)); err == nil && json.Unmarshal(b, &o) == nil &&
		(o.Owner == OwnerApp || o.Owner == OwnerHeadless) {
		return o.Owner
	}
	return OwnerApp
}

// SetOwner records the owner.
func (r Runtime) SetOwner(owner string) error {
	if owner != OwnerApp && owner != OwnerHeadless {
		return fmt.Errorf("owner %q: %s or %s", owner, OwnerApp, OwnerHeadless)
	}
	if err := os.MkdirAll(r.Root, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(Owner{Owner: owner})
	return os.WriteFile(filepath.Join(r.Root, ownerFile), append(b, '\n'), 0o644)
}

// Status is `runtime status`.
type Status struct {
	Root      string `json:"root"`
	Current   string `json:"current,omitempty"`
	Version   string `json:"version,omitempty"`
	Previous  string `json:"previous,omitempty"`
	Watermark string `json:"watermark,omitempty"`
	Owner     string `json:"owner"`
	Installed []Dir  `json:"installed"`
	// From is the payload this binary runs from, when it has one: its directory
	// name and version. UpdatePending: it is newer than current (or the same
	// version built again), so the app activates it. Downgrade: it is older than
	// the watermark, so an install of it is refused.
	From          string `json:"from,omitempty"`
	FromVersion   string `json:"from_version,omitempty"`
	UpdatePending bool   `json:"update_pending"`
	Downgrade     bool   `json:"downgrade"`
}

// Status describes the runtime; from is a payload to compare with ("" none).
func (r Runtime) Status(from string) (Status, error) {
	st := Status{Root: r.Root, Owner: r.ReadOwner(), Installed: []Dir{}, Watermark: r.Watermark(),
		Previous: r.Previous()}
	cur, err := r.Current()
	if err != nil {
		return st, err
	}
	st.Current = cur
	dirs, err := r.List()
	if err != nil {
		return st, err
	}
	st.Installed = append(st.Installed, dirs...)
	for _, d := range dirs {
		if d.Name == cur {
			st.Version = d.Version
		}
	}
	if from == "" {
		return st, nil
	}
	p, err := ReadPayload(from)
	if err != nil {
		return st, nil
	}
	st.From, st.FromVersion = DirName(p), p.Version
	floor := st.Watermark
	if floor == "" {
		floor = st.Version
	}
	switch {
	case floor != "" && layout.CompareVersions(p.Version, floor) < 0:
		st.Downgrade = true
	case st.From == cur:
	case cur == "", layout.CompareVersions(p.Version, st.Version) >= 0:
		st.UpdatePending = true
	}
	return st, nil
}
