//go:build !linux

package discovery

// Only Linux has systemd-resolved (announce_resolved_linux.go).
// The same file is in api/internal/discovery and cmd/maktaba-server/internal/mdns (two
// modules); tools/ci/mdns_copies.py checks they stay the same.

import "errors"

func probeResolved() resolvedState { return resolvedState{} }

func newResolvedAPI() (resolvedAPI, error) { return nil, errors.New("no systemd-resolved here") }
