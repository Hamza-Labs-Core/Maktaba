package main

import "testing"

// TestStub keeps `go test ./...` from exiting 5 on the empty stub
// module. Real tests land with Story 08.
func TestStub(t *testing.T) {
	t.Parallel()
}
