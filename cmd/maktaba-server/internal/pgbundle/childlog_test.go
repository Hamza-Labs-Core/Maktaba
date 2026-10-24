package pgbundle

import (
	"os"
	"strings"
	"testing"
)

// A program given serve's stamped stderr writes to serve's original one (its lines carry
// their own times); any other log is kept.
func TestChildLog(t *testing.T) {
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	os.Stderr = w
	ChildOutput = orig
	defer func() { os.Stderr, ChildOutput = orig, nil }()
	if childLog(w) != orig {
		t.Fatal("serve's stamped stderr not swapped for the original")
	}
	var b strings.Builder
	if childLog(&b) != &b {
		t.Fatal("another log not kept")
	}
	ChildOutput = nil
	if childLog(w) != w {
		t.Fatal("no stamping: kept as given")
	}
}
