// Package release parses the release manifest (`deploy/packaging/release-manifest.json`).
// The CLI under `cmd/` validates the manifest as part of the release
// workflow (Story 22.5 AC-2 + AC-4).
package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// Manifest mirrors the JSON schema.
type Manifest struct {
	Version           string               `json:"version"`
	GitSHA            string               `json:"git_sha"`
	BuiltAt           time.Time            `json:"built_at"`
	SchemaRev         int                  `json:"schema_rev"`
	Components        map[string]Component `json:"components"`
	RollbackTo        string               `json:"rollback_to"`
	RollbackSchemaRev int                  `json:"rollback_schema_rev"`
}

// Component is one published image / package.
type Component struct {
	Image  string `json:"image"`
	SHA256 string `json:"sha256"`
}

// Load reads + validates the manifest at path.
func Load(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

var (
	semverRE = regexp.MustCompile(`^\d+\.\d+\.\d+(-[A-Za-z0-9.-]+)?$`)
	tagRE    = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	shaRE    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Validate enforces shape invariants.
func (m *Manifest) Validate() error {
	if !semverRE.MatchString(m.Version) {
		return fmt.Errorf("version %q: not semver", m.Version)
	}
	if !shaRE.MatchString(m.GitSHA) {
		return fmt.Errorf("git_sha %q: not 40 hex chars", m.GitSHA)
	}
	if m.BuiltAt.IsZero() {
		return errors.New("built_at: required")
	}
	if m.SchemaRev <= 0 {
		return errors.New("schema_rev: must be > 0")
	}
	if len(m.Components) == 0 {
		return errors.New("components: required")
	}
	required := []string{"api", "streaming", "pipeline", "web"}
	for _, k := range required {
		c, ok := m.Components[k]
		if !ok {
			return fmt.Errorf("components.%s: required", k)
		}
		if !strings.Contains(c.Image, ":") {
			return fmt.Errorf("components.%s.image: missing tag", k)
		}
		if !digestRE.MatchString(c.SHA256) {
			return fmt.Errorf("components.%s.sha256: bad digest", k)
		}
	}
	if m.RollbackTo != "" && !tagRE.MatchString(m.RollbackTo) {
		return fmt.Errorf("rollback_to %q: must be a vMAJOR.MINOR.PATCH tag", m.RollbackTo)
	}
	return nil
}
