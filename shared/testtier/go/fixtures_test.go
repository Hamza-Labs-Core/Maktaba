package testtier

import (
	"os"
	"strings"
	"testing"
)

func TestNewIDStable(t *testing.T) {
	a := NewID(t, "v")
	b := NewID(t, "v")
	if a != b {
		t.Fatalf("NewID not stable: %s vs %s", a, b)
	}
}

func TestNewIDIsUUIDish(t *testing.T) {
	id := NewID(t, "x")
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Fatalf("not UUID-ish: %s", id)
	}
}

func TestNewIDKindsDiffer(t *testing.T) {
	if NewID(t, "video") == NewID(t, "library") {
		t.Fatal("different kinds should yield different ids")
	}
}

func TestSkipIfNoDBSkipsWhenUnset(t *testing.T) {
	_ = os.Unsetenv("MAKTABA_TEST_DSN")
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	// Sub-test so we can observe the skip without skipping this test.
	t.Run("inner", func(tt *testing.T) {
		SkipIfNoDB(tt)
		tt.Fatal("should have skipped")
	})
}

func TestMustEnvSet(t *testing.T) {
	t.Setenv("FOO", "bar")
	if got := MustEnv(t, "FOO"); got != "bar" {
		t.Fatal("MustEnv lost the value")
	}
}
