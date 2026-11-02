//go:build !linux

package mdns

// Only Linux has systemd-resolved (announce_resolved_linux.go).
// api/internal/discovery has the same file (another module).

import "errors"

func probeResolved() resolvedState { return resolvedState{} }

func newResolvedAPI() (resolvedAPI, error) { return nil, errors.New("no systemd-resolved here") }
