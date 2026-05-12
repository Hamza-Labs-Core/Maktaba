package security

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Story 23.7 — supply-chain surface.
//
// We bundle a minimal SBOM (CycloneDX 1.5 subset) at build time so the
// running binary can answer `GET /api/system/sbom` without going back
// to the filesystem. The CI build embeds the JSON via go:embed; for
// tests, Load reads a file.
//
// We don't validate the full CycloneDX schema — only the fields that
// downstream consumers (Dependency-Track, the security review job) need.

// SBOM is the parsed top-level document.
type SBOM struct {
	BOMFormat   string      `json:"bomFormat"`
	SpecVersion string      `json:"specVersion"`
	Version     int         `json:"version"`
	Metadata    SBOMMeta    `json:"metadata"`
	Components  []Component `json:"components"`
}

// SBOMMeta is the metadata block.
type SBOMMeta struct {
	Timestamp string     `json:"timestamp"`
	Tools     []SBOMTool `json:"tools"`
	Component *Component `json:"component,omitempty"`
}

// SBOMTool identifies the SBOM generator.
type SBOMTool struct {
	Vendor  string `json:"vendor"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Component is one supply-chain entry.
type Component struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Version string `json:"version"`
	PURL    string `json:"purl,omitempty"`
	License string `json:"license,omitempty"`
}

// LoadSBOM parses the bytes into an SBOM and validates structural invariants.
func LoadSBOM(b []byte) (*SBOM, error) {
	var s SBOM
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("sbom: parse: %w", err)
	}
	if s.BOMFormat == "" || !strings.EqualFold(s.BOMFormat, "CycloneDX") {
		return nil, errors.New("sbom: bomFormat must be CycloneDX")
	}
	if s.SpecVersion == "" {
		return nil, errors.New("sbom: specVersion required")
	}
	if s.Version <= 0 {
		return nil, errors.New("sbom: version must be > 0")
	}
	return &s, nil
}

// Summary returns aggregate counts for quick UI display.
type Summary struct {
	TotalComponents int
	ByType          map[string]int
	UniqueLicenses  []string
}

// Summary computes aggregates from the SBOM.
func (s *SBOM) Summary() Summary {
	out := Summary{ByType: map[string]int{}}
	licenses := map[string]bool{}
	for _, c := range s.Components {
		out.TotalComponents++
		out.ByType[c.Type]++
		if c.License != "" {
			licenses[c.License] = true
		}
	}
	for l := range licenses {
		out.UniqueLicenses = append(out.UniqueLicenses, l)
	}
	sort.Strings(out.UniqueLicenses)
	return out
}

// FindByPURL returns the component whose PURL matches.
func (s *SBOM) FindByPURL(purl string) *Component {
	for i, c := range s.Components {
		if c.PURL == purl {
			return &s.Components[i]
		}
	}
	return nil
}
