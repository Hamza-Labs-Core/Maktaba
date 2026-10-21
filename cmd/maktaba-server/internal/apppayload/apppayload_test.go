package apppayload

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		want  int
		known bool
	}{
		{"v0.3.0", "v0.2.0", 1, true},
		{"0.2.0", "v0.2.0", 0, true},
		{"v0.3.0-rc.1", "v0.3.0", -1, true},
		{"v0.3.0-rc.2", "v0.3.0-rc.1", 1, true},
		{"v0.3.0-beta.9", "v0.3.0-rc.1", -1, true},
		{"v1.10.0", "v1.9.9", 1, true},
		{"v0.2.0-12-gabc1234", "v0.2.0", 0, false}, // git describe: a development build
		{"dev", "v0.2.0", 0, false},
	} {
		got, known := CompareVersions(c.a, c.b)
		if got != c.want || known != c.known {
			t.Errorf("CompareVersions(%q, %q) = %d %v, want %d %v", c.a, c.b, got, known, c.want, c.known)
		}
	}
}

func TestCompare(t *testing.T) {
	p := func(v string) Payload { var x Payload; x.Version = v; return x }
	if c := Compare(Payload{}, false, p("v0.3.0")); c.Upgrade || c.Downgrade {
		t.Errorf("the first start is neither: %+v", c)
	}
	if c := Compare(p("v0.3.0"), true, p("v0.3.0")); c.Upgrade || c.Downgrade {
		t.Errorf("the same version: %+v", c)
	}
	if c := Compare(p("v0.2.0"), true, p("v0.3.0")); !c.Upgrade || c.From != "v0.2.0" || c.To != "v0.3.0" {
		t.Errorf("an update: %+v", c)
	}
	if c := Compare(p("v0.3.0"), true, p("v0.2.0")); !c.Downgrade || c.Upgrade {
		t.Errorf("an older release: %+v", c)
	}
	// a development build's order is unknown: dumped as an update, never refused
	if c := Compare(p("v0.3.0"), true, p("v0.2.0-5-gdeadbee")); !c.Upgrade || c.Downgrade {
		t.Errorf("a development build: %+v", c)
	}
}

func TestConfirmAndRead(t *testing.T) {
	payload, state := t.TempDir(), t.TempDir()
	body := `{"schema":1,"version":"v0.3.0","platform":"windows-x86_64","postgres":{"version":"16.15.0"},` +
		`"python":{"version":"3.12.15"},"pipeline":{"wheel":"w.whl","sha256":"aa","constraints_sha256":"bb"}}`
	if err := os.WriteFile(filepath.Join(payload, "payload.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Confirmed(state); ok || err != nil {
		t.Fatalf("nothing confirmed yet: %v %v", ok, err)
	}
	if err := Confirm(state, payload); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Confirmed(state)
	if err != nil || !ok || got.Version != "v0.3.0" || got.PostgresMajor() != "16" ||
		got.Pipeline.ConstraintsSHA256 != "bb" || got.Python.Version != "3.12.15" {
		t.Fatalf("confirmed = %+v %v %v", got, ok, err)
	}
}
