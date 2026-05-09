package main

import "testing"

// TestStub keeps `go test ./...` from exiting 5 (no tests collected) on
// the empty stub package. Real tests land with Story 07.
//
// Logger wiring lives in shared/log/go and is covered by that module's
// tests; main() itself is too thin to be worth testing here.
func TestStub(t *testing.T) {
	t.Parallel()
}
