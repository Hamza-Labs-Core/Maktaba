package main

import "testing"

// TestStub keeps `go test ./...` from exiting 5 on the empty stub
// package. Real tests land with Story 08.
//
// Logger wiring lives in shared/log/go and is covered by that module's
// tests.
func TestStub(t *testing.T) {
	t.Parallel()
}
