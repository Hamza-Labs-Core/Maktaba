// Package apppayload reads the desktop app's payload.json (what the server
// payload carries: its version, the Postgres, Python and pipeline inside) and
// keeps the copy of the last one that ran healthy, <state>/state/payload.json.
// Comparing the two at the server's start tells it what an update changed
// (specs/epics/38-windows-app/contract-update.md, Epic 36's contract-update.md):
// a new version takes a dump of the database before its migrations, an older
// one is refused over a database a newer one has run.
package apppayload

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Payload is the part of payload.json the server reads.
type Payload struct {
	Schema   int    `json:"schema"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Postgres struct {
		Version string `json:"version"`
	} `json:"postgres"`
	Python struct {
		Version string `json:"version"`
	} `json:"python"`
	Pipeline struct {
		Wheel             string `json:"wheel"`
		SHA256            string `json:"sha256"`
		ConstraintsSHA256 string `json:"constraints_sha256"`
	} `json:"pipeline"`
}

// PostgresMajor is the bundled Postgres's major version ("16" of "16.15.0").
func (p Payload) PostgresMajor() string {
	major, _, _ := strings.Cut(p.Postgres.Version, ".")
	return major
}

// Read reads a payload.json.
func Read(path string) (Payload, error) {
	var p Payload
	raw, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// StatePath is the copy of the last payload that ran healthy.
func StatePath(stateDir string) string { return filepath.Join(stateDir, "state", "payload.json") }

// Confirmed is the last payload that ran healthy; ok false before the first.
func Confirmed(stateDir string) (Payload, bool, error) {
	p, err := Read(StatePath(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return Payload{}, false, nil
	}
	return p, err == nil, err
}

// Confirm saves payloadDir's payload.json as the last one that ran healthy.
func Confirm(stateDir, payloadDir string) error {
	raw, err := os.ReadFile(filepath.Join(payloadDir, "payload.json"))
	if err != nil {
		return err
	}
	return layout.WriteFileAtomic(StatePath(stateDir), raw, 0o644)
}

// Change is what an update changed since the last healthy start.
type Change struct {
	From, To string
	// Upgrade: another version than the one that ran (a newer one, or one whose
	// order cannot be told: a development build). The database is dumped
	// before the new version's migrations.
	Upgrade bool
	// Downgrade: an older release than the one that ran; its code does not know
	// the newer one's migrations, so the API is not started.
	Downgrade bool
}

// Compare tells what changed from the confirmed payload to the current one.
func Compare(confirmed Payload, ok bool, current Payload) Change {
	c := Change{From: confirmed.Version, To: current.Version}
	if !ok || confirmed.Version == current.Version || confirmed.Version == "" {
		return c
	}
	if cmp, known := CompareVersions(current.Version, confirmed.Version); known && cmp < 0 {
		c.Downgrade = true
		return c
	}
	c.Upgrade = true
	return c
}

// release is a release tag: v1.2.3, or a pre-release v1.2.3-rc.1 (as
// tools/release payload scripts accept them).
var release = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-(rc|alpha|beta|a|b)\.?(\d+))?$`)

// CompareVersions orders two release versions (-1, 0, 1); known false when
// either is not a release (a development build).
func CompareVersions(a, b string) (int, bool) {
	pa, ok1 := parse(a)
	pb, ok2 := parse(b)
	if !ok1 || !ok2 {
		return 0, false
	}
	for i := range pa {
		switch {
		case pa[i] < pb[i]:
			return -1, true
		case pa[i] > pb[i]:
			return 1, true
		}
	}
	return 0, true
}

// parse: major, minor, patch, then the pre-release's rank (alpha 1, beta 2,
// rc 3, a release 4) and number.
func parse(v string) ([5]int, bool) {
	m := release.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return [5]int{}, false
	}
	var out [5]int
	for i := 0; i < 3; i++ {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	switch m[4] {
	case "":
		out[3] = 4
	case "alpha", "a":
		out[3] = 1
	case "beta", "b":
		out[3] = 2
	case "rc":
		out[3] = 3
	}
	out[4], _ = strconv.Atoi(m[5])
	return out, true
}
