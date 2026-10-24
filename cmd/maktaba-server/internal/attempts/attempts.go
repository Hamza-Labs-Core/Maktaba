// Package attempts keeps a machine's update attempts, every failure with its reason
// (specs/epics/35-cluster-full-nodes-and-updates/update-attempts.md): the desktop app's
// own updater writes app-attempts.json, the server a release's starts in
// server-attempts.json, both in the machine's updates folder. The server's update status
// carries both, so the Updates page shows why an update failed, whoever tried it.
package attempts

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// The two files, one writer each.
const (
	AppFile    = "app-attempts.json"
	ServerFile = "server-attempts.json"
)

// Keep is how many attempts each file keeps; Show how many the status carries.
const (
	Keep = 20
	Show = 10
)

// Sources.
const (
	SourceApp    = "app"
	SourceServer = "server"
)

// Outcomes.
const (
	Running   = "running"
	Installed = "installed"
	Failed    = "failed"
	PutBack   = "put_back"
)

// Attempt is one try at bringing the machine to a release.
type Attempt struct {
	ID      string     `json:"id"`
	At      time.Time  `json:"at"`
	EndedAt *time.Time `json:"ended_at,omitempty"`
	Source  string     `json:"source"`
	Trigger string     `json:"trigger,omitempty"`
	From    string     `json:"from,omitempty"`
	To      string     `json:"to"`
	Outcome string     `json:"outcome"`
	Step    string     `json:"step,omitempty"`
	Code    string     `json:"code,omitempty"`
	Detail  string     `json:"detail,omitempty"`
	Fix     string     `json:"fix,omitempty"`
}

type file struct {
	Attempts []Attempt `json:"attempts"`
}

func readFile(path string) []Attempt {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	// one entry another build wrote in a way this one cannot read costs that entry only
	var f struct {
		Attempts []json.RawMessage `json:"attempts"`
	}
	if json.Unmarshal(b, &f) != nil {
		return nil
	}
	out := make([]Attempt, 0, len(f.Attempts))
	for _, raw := range f.Attempts {
		var a Attempt
		if json.Unmarshal(raw, &a) == nil {
			out = append(out, a)
		}
	}
	return out
}

// Read is the machine's attempts, both files, newest first, at most Show.
func Read(dir string) []Attempt {
	if dir == "" {
		return nil
	}
	all := append(readFile(filepath.Join(dir, AppFile)), readFile(filepath.Join(dir, ServerFile))...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.After(all[j].At) })
	if len(all) > Show {
		all = all[:Show]
	}
	return all
}

// Stamp is the latest change of either file ("" when neither is there): a node's agent
// sends its state again when it moves.
func Stamp(dir string) string {
	var parts []string
	for _, n := range []string{AppFile, ServerFile} {
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil {
			parts = append(parts, st.ModTime().UTC().Format(time.RFC3339Nano))
		}
	}
	return strings.Join(parts, "|")
}

// Record puts a into the server's file in its place (the same id is replaced, a new one
// goes first), keeping Keep.
func Record(dir string, a Attempt) error {
	return change(dir, func(list []Attempt) []Attempt {
		for i := range list {
			if list[i].ID == a.ID {
				list[i] = a
				return list
			}
		}
		return append([]Attempt{a}, list...)
	})
}

// Update changes the server's attempt id in place, when there is one; it reports whether
// there was.
func Update(dir, id string, fn func(a *Attempt)) (bool, error) {
	found := false
	err := change(dir, func(list []Attempt) []Attempt {
		for i := range list {
			if list[i].ID == id {
				fn(&list[i])
				found = true
			}
		}
		return list
	})
	return found, err
}

// Find is the server's newest attempt at release to (ok false when there is none).
func Find(dir, to string) (Attempt, bool) {
	to = layout.NormalizeVersion(to)
	for _, a := range readFile(filepath.Join(dir, ServerFile)) {
		if layout.NormalizeVersion(a.To) == to {
			return a, true
		}
	}
	return Attempt{}, false
}

func change(dir string, fn func([]Attempt) []Attempt) error {
	if dir == "" {
		return errors.New("no updates folder")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, ServerFile)
	list := fn(readFile(path))
	if len(list) > Keep {
		list = list[:Keep]
	}
	b, err := json.MarshalIndent(file{Attempts: list}, "", "  ")
	if err != nil {
		return err
	}
	return layout.WriteFileAtomic(path, append(b, '\n'), 0o644)
}

// NewStartID is the id of a release's start on this machine, begun now.
func NewStartID(version string) string {
	return "start-" + layout.NormalizeVersion(version) + "-" + strconv.FormatInt(Now().UnixMilli(), 10)
}

// Now is the attempts' clock (tests replace it).
var Now = func() time.Time { return time.Now().UTC() }

// Ended marks a ended now with outcome.
func (a *Attempt) Ended(outcome string) {
	now := Now()
	a.Outcome, a.EndedAt = outcome, &now
}
