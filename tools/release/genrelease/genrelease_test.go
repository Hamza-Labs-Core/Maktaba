package genrelease

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

const repoRoot = "../../.."

// The checkout itself: migrations, SCHEMA_REV and SchemaRev agree (AC2).
func TestReadRepoCheckout(t *testing.T) {
	f, err := ReadRepo(repoRoot, 0)
	if err != nil {
		t.Fatal(err)
	}
	if f.SchemaRev < 128 || f.MinNodeSchema < 128 || f.MinNodeSchema > f.SchemaRev {
		t.Fatalf("facts = %+v", f)
	}
	if f.EdgeProtocol < 1 || f.AgentProtocol < 1 || f.Python == "" {
		t.Fatalf("facts = %+v", f)
	}
}

func TestMarkers(t *testing.T) {
	md := strings.Join([]string{
		"| `0120` | `0120_x.sql` | p | — | adds x. **Breaking for older nodes**: `maktaba_min_node_schema()` returns 118. |",
		"| `0125` | `0125_y.sql` | p | — | adds y. **Breaking for older nodes** (no value given). |",
		"| `0126` | `0126_z.sql` | p | — | drops q. **Breaking for the previous release**. |",
		"| `0127` | `0127_w.sql` | p | — | nothing special |",
	}, "\n")
	minNode, prev, err := markers(md, 0)
	if err != nil || minNode != 125 || !prev {
		t.Fatalf("markers(prev=0) = %d, %v, %v", minNode, prev, err)
	}
	if _, prev, _ := markers(md, 126); prev {
		t.Fatal("a breaking row at or below the previous release's schema_rev counted")
	}
	if _, _, err := markers("| `0100` | f | p | — | **Breaking for older nodes**: `maktaba_min_node_schema()` returns 101 |", 0); err == nil {
		t.Fatal("min node above its own slot accepted")
	}
}

func fakeRepo(t *testing.T, py, gov int) string {
	t.Helper()
	dir := t.TempDir()
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(MigrationsDir+"/0007_a.sql", "--")
	write(MigrationsDir+"/0008_b.sql", "--")
	write(MigrationsDir+"/0009_c.sqlite.sql", "--") // sqlite twins are not slots of their own
	write(ManifestMD, "| `0008` | `0008_b.sql` | p | — | **Breaking for older nodes**: `maktaba_min_node_schema()` returns 8. |\n")
	write(SchemaPy, "SCHEMA_REV = "+itoa(py)+"\n")
	write(SchemaGo, "const SchemaRev = "+itoa(gov)+"\n")
	write(EdgeProtocolGo, "const EdgeProtocol = 3\n")
	write(LauncherGo, "const LauncherProtocol = 1\n")
	write(AgentProtocolGo, "const (\n\tProtocolVersion = 2\n)\n")
	write(PythonVersion, "3.12.4\n")
	return dir
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestReadRepoSchemaMismatch(t *testing.T) {
	if _, err := ReadRepo(fakeRepo(t, 8, 8), 0); err != nil {
		t.Fatalf("consistent repo: %v", err) // 0009 is a sqlite twin only: highest slot 8
	}
	for _, c := range [][2]int{{7, 8}, {8, 9}} {
		if _, err := ReadRepo(fakeRepo(t, c[0], c[1]), 0); err == nil || !strings.Contains(err.Error(), "disagree") {
			t.Fatalf("SCHEMA_REV %d / SchemaRev %d accepted: %v", c[0], c[1], err)
		}
	}
}

func TestBuildAndCheck(t *testing.T) {
	repo := fakeRepo(t, 8, 8)
	ss := releaseverifytest.MustNew()
	dist := t.TempDir()
	rels := map[string]*releaseverifytest.FakeRelease{}
	for _, p := range []string{"linux/amd64", "windows/amd64"} {
		rel := ss.MustRelease("Hamza-Labs-Core/Maktaba", "v1.5.0", p)
		rels[p] = rel
		for _, n := range []string{rel.Archive, rel.Wheel, rel.Cons} {
			if err := os.WriteFile(filepath.Join(dist, n), rel.Files[n], 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	m, err := Build(Options{Tag: "v1.5.0", Commit: strings.Repeat("ab", 20), Dist: dist, Repo: repo,
		Published: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	for p, rel := range rels {
		for name, b := range rel.Binaries {
			if err := m.VerifyExtracted(p, name, b); err != nil {
				t.Fatalf("%s %s: %v (files %v)", p, name, err, m.Assets[p].Files)
			}
		}
	}
	if m.SchemaRev != 8 || m.MinNodeSchema != 8 || m.EdgeProtocol != 3 || m.AgentProtocol != 2 ||
		m.LauncherProtocol != 1 || m.MinUpgradeFrom != "1.5.0" || m.Channel != "stable" || m.Pipeline.Python != "3.12" {
		t.Fatalf("manifest = %+v", m)
	}
	if err := Check(m, repo, 0); err != nil {
		t.Fatalf("Check: %v", err)
	}
	m.MinNodeSchema = 7
	if err := Check(m, repo, 0); err == nil || !strings.Contains(err.Error(), "min_node_schema") {
		t.Fatalf("Check accepted a wrong min_node_schema: %v", err)
	}
}

func TestBuildCarriesMinUpgradeFrom(t *testing.T) {
	repo := fakeRepo(t, 8, 8)
	ss := releaseverifytest.MustNew()
	dist := t.TempDir()
	rel := ss.MustRelease("Hamza-Labs-Core/Maktaba", "v1.6.0-rc.1", "linux/arm64")
	if err := os.WriteFile(filepath.Join(dist, rel.Archive), rel.Files[rel.Archive], 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Build(Options{Tag: "v1.6.0-rc.1", Commit: strings.Repeat("cd", 20), Dist: dist, Repo: repo,
		Previous: &rv.Manifest{SchemaRev: 7, MinUpgradeFrom: "1.5.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if m.MinUpgradeFrom != "1.5.0" || m.Channel != "beta" || m.Pipeline != nil {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestBuildRejectsWrongArchiveVersion(t *testing.T) {
	repo := fakeRepo(t, 8, 8)
	ss := releaseverifytest.MustNew()
	dist := t.TempDir()
	rel := ss.MustRelease("Hamza-Labs-Core/Maktaba", "v1.4.0", "linux/amd64")
	if err := os.WriteFile(filepath.Join(dist, rel.Archive), rel.Files[rel.Archive], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(Options{Tag: "v1.5.0", Commit: strings.Repeat("ab", 20), Dist: dist, Repo: repo}); err == nil {
		t.Fatal("archive of another version accepted")
	}
}
