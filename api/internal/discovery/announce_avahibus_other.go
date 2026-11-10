//go:build !linux

package discovery

// Only a Linux container is given the host's system bus (announce_avahibus_linux.go).
// The same file is in api/internal/discovery and cmd/maktaba-server/internal/mdns (two
// modules); tools/ci/mdns_copies.py checks they stay the same.

func avahiOnBus() bool { return false }
