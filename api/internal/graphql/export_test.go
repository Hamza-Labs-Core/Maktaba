package graphql

import (
	"os"
	"path/filepath"
	"testing"
)

// sharedSchemaPath is the committed source-of-truth SDL the spec
// (architecture.md §, plan-07-17, plan-15-03) and the Android Apollo
// Gradle plugin (apps/tv/android/app/build.gradle.kts) both reference.
// It must stay byte-identical to the embedded Go `Schema` const.
const sharedSchemaPath = "../../../shared/graphql/schema.graphql"

// TestSharedSchemaInSync regenerates shared/graphql/schema.graphql from
// the Go const and fails if the committed copy drifted. Run
//
//	GRAPHQL_WRITE_SCHEMA=1 go test ./internal/graphql/...
//
// to (re)write it after an intentional SDL change.
func TestSharedSchemaInSync(t *testing.T) {
	abs, err := filepath.Abs(sharedSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("GRAPHQL_WRITE_SCHEMA") == "1" {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(Schema), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", abs)
		return
	}
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("shared SDL missing (%v); regenerate with GRAPHQL_WRITE_SCHEMA=1", err)
	}
	if string(got) != Schema {
		t.Fatalf("%s is stale; regenerate with GRAPHQL_WRITE_SCHEMA=1", sharedSchemaPath)
	}
}
