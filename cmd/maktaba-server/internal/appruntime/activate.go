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
)

// CurrentLink is the symlink naming the runtime the service runs.
const CurrentLink = "current"

const ownerFile = "owner.json"

// Service is the unit that runs the runtime (the systemd user service).
type Service interface {
	// Active reports whether it runs now (an installed unit that is active or starting).
	Active(ctx context.Context) bool
	Stop(ctx context.Context) error
	Start(ctx context.Context) error
}

// Current is the directory name `current` points at ("" when there is none).
func (r Runtime) Current() (string, error) {
	t, err := os.Readlink(filepath.Join(r.Root, CurrentLink))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return filepath.Base(t), err
}

// Activate points `current` at an installed directory with the service
// stopped: it stops a running service, moves the link, and starts the service
// again (a stopped one, paused by the user, stays stopped and runs the new
// runtime when resumed). A start that fails puts the old link back and starts
// that. svc may be nil (no service installed yet).
func (r Runtime) Activate(ctx context.Context, name string, svc Service) (previous string, err error) {
	name = filepath.Base(name)
	inst, err := readInstalled(filepath.Join(r.Root, name))
	if err != nil {
		return "", fmt.Errorf("%s is not an installed runtime: %w", filepath.Join(r.Root, name), err)
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
	running := svc != nil && svc.Active(ctx)
	if running {
		if err := svc.Stop(ctx); err != nil {
			return previous, fmt.Errorf("stop the service: %w", err)
		}
	}
	if err := r.point(name); err != nil {
		if running {
			_ = svc.Start(ctx)
		}
		return previous, err
	}
	if running {
		if err := svc.Start(ctx); err != nil {
			if previous != "" {
				_ = r.point(previous)
				_ = svc.Start(ctx)
			}
			return previous, fmt.Errorf("start the service on %s (%s is back): %w", name, previous, err)
		}
	}
	return previous, nil
}

// point moves `current` to name: a new link renamed over the old (atomic).
func (r Runtime) point(name string) error {
	tmp := filepath.Join(r.Root, CurrentLink+".new")
	_ = os.Remove(tmp)
	if err := os.Symlink(name, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(r.Root, CurrentLink)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	r.sync()
	return nil
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

// Prune keeps the current runtime and the keep-1 newest others, and removes
// the rest (and what an install cut short left). It returns what it removed.
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
	var removed []string
	kept := 0
	if cur != "" {
		kept = 1
	}
	for _, d := range dirs {
		if d.Name == cur {
			continue
		}
		if kept < keep {
			kept++
			continue
		}
		if err := os.RemoveAll(filepath.Join(r.Root, d.Name)); err != nil {
			return removed, err
		}
		removed = append(removed, d.Name)
	}
	return removed, nil
}

// Owner is who updates the runtime: "app" (the desktop shell, through its
// updater) or "headless" (the machine itself, Epic 35's verified releases).
type Owner struct {
	Owner string `json:"owner"`
}

// ReadOwner is the runtime's owner ("app" when none was recorded).
func (r Runtime) ReadOwner() string {
	var o Owner
	if b, err := os.ReadFile(filepath.Join(r.Root, ownerFile)); err == nil && json.Unmarshal(b, &o) == nil &&
		(o.Owner == "app" || o.Owner == "headless") {
		return o.Owner
	}
	return "app"
}

// SetOwner records the owner.
func (r Runtime) SetOwner(owner string) error {
	if owner != "app" && owner != "headless" {
		return fmt.Errorf("owner %q: app or headless", owner)
	}
	if err := os.MkdirAll(r.Root, 0o755); err != nil {
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
	Owner     string `json:"owner"`
	Installed []Dir  `json:"installed"`
	// From is the payload this binary runs from, when it has one: its directory
	// name, and whether it differs from current (an update to activate).
	From          string `json:"from,omitempty"`
	FromVersion   string `json:"from_version,omitempty"`
	UpdatePending bool   `json:"update_pending"`
}

// Status describes the runtime; from is a payload to compare with ("" none).
func (r Runtime) Status(from string) (Status, error) {
	st := Status{Root: r.Root, Owner: r.ReadOwner(), Installed: []Dir{}}
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
		switch {
		case d.Name == cur:
			st.Version = d.Version
		case st.Previous == "":
			st.Previous = d.Name
		}
	}
	if from != "" {
		if p, err := ReadPayload(from); err == nil {
			st.From, st.FromVersion = DirName(p), p.Version
			st.UpdatePending = st.From != cur
		}
	}
	return st, nil
}
