package metrics

import (
	"strings"
	"testing"
)

func TestDashboardManifestNonEmpty(t *testing.T) {
	if len(DashboardManifest) == 0 {
		t.Fatal("expected at least one dashboard")
	}
	seen := map[string]bool{}
	for _, d := range DashboardManifest {
		if d.ID == "" || d.File == "" {
			t.Fatalf("incomplete entry: %+v", d)
		}
		if seen[d.ID] {
			t.Fatalf("duplicate dashboard id: %s", d.ID)
		}
		seen[d.ID] = true
		if !strings.HasSuffix(d.File, ".json") {
			t.Fatalf("file must be .json: %s", d.File)
		}
	}
}

func TestDashboardByID(t *testing.T) {
	if d := DashboardByID("api-latency"); d == nil || d.Title == "" {
		t.Fatalf("missing api-latency dashboard")
	}
	if DashboardByID("nope") != nil {
		t.Fatal("DashboardByID should miss")
	}
}

func TestAlertManifestSeverities(t *testing.T) {
	for _, a := range AlertManifest {
		switch a.Severity {
		case "page", "warn", "info":
		default:
			t.Fatalf("invalid severity %q for %s", a.Severity, a.Name)
		}
		if a.Expression == "" {
			t.Fatalf("alert %s missing expression", a.Name)
		}
		if a.ForSec < 0 {
			t.Fatalf("alert %s negative ForSec", a.Name)
		}
	}
}

func TestAlertNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range AlertManifest {
		if seen[a.Name] {
			t.Fatalf("duplicate alert name: %s", a.Name)
		}
		seen[a.Name] = true
	}
}
