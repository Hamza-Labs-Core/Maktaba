package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repeat(s string, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = s[0]
	}
	return string(out)
}

func validJSON() string {
	return `{
  "version": "1.2.3",
  "git_sha": "0123456789abcdef0123456789abcdef01234567",
  "built_at": "2026-05-10T12:00:00Z",
  "schema_rev": 57,
  "components": {
    "api":       {"image": "registry/api:1.2.3", "sha256": "sha256:` + repeat("a", 64) + `"},
    "streaming": {"image": "registry/streaming:1.2.3", "sha256": "sha256:` + repeat("b", 64) + `"},
    "pipeline":  {"image": "registry/pipeline:1.2.3", "sha256": "sha256:` + repeat("c", 64) + `"},
    "web":       {"image": "registry/web:1.2.3", "sha256": "sha256:` + repeat("d", 64) + `"}
  },
  "rollback_to": "v1.2.2",
  "rollback_schema_rev": 56
}`
}

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "m.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValid(t *testing.T) {
	m, err := Load(writeTemp(t, validJSON()))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "1.2.3" {
		t.Fatalf("version: %s", m.Version)
	}
	if m.SchemaRev != 57 {
		t.Fatalf("schema_rev: %d", m.SchemaRev)
	}
}

func TestValidateRejectsBadSemver(t *testing.T) {
	bad := strings.Replace(validJSON(), `"1.2.3"`, `"1.2"`, 1)
	_, err := Load(writeTemp(t, bad))
	if err == nil || !strings.Contains(err.Error(), "semver") {
		t.Fatalf("expected semver rejection, got %v", err)
	}
}

func TestValidateRejectsBadSHA(t *testing.T) {
	bad := strings.Replace(validJSON(), "0123456789abcdef0123456789abcdef01234567", "deadbeef", 1)
	_, err := Load(writeTemp(t, bad))
	if err == nil {
		t.Fatal("expected git_sha rejection")
	}
}

func TestValidateRejectsMissingComponent(t *testing.T) {
	bad := strings.Replace(validJSON(), `"web":`, `"_web":`, 1)
	_, err := Load(writeTemp(t, bad))
	if err == nil {
		t.Fatal("expected missing-component rejection")
	}
}

func TestValidateRejectsBadDigest(t *testing.T) {
	bad := strings.Replace(validJSON(), "sha256:"+repeat("a", 64), "sha256:nope", 1)
	_, err := Load(writeTemp(t, bad))
	if err == nil {
		t.Fatal("expected digest rejection")
	}
}

func TestValidateRejectsBadRollbackTag(t *testing.T) {
	bad := strings.Replace(validJSON(), `"v1.2.2"`, `"1.2.2"`, 1)
	_, err := Load(writeTemp(t, bad))
	if err == nil {
		t.Fatal("expected rollback tag rejection")
	}
}

func TestLoadCanonicalRepoManifest(t *testing.T) {
	// Skip if path doesn't resolve (running outside the repo).
	p := "../../deploy/packaging/release-manifest.json"
	if _, err := os.Stat(p); os.IsNotExist(err) {
		t.Skip("release-manifest.json not present in this layout")
	}
	if _, err := Load(p); err != nil {
		t.Fatalf("real manifest invalid: %v", err)
	}
}
