package main

import "testing"

// TestStub keeps `go test ./...` from exiting 5 (no tests collected) on
// the empty stub module. Real tests land with Story 07.
func TestStub(t *testing.T) {
	t.Parallel()
}
