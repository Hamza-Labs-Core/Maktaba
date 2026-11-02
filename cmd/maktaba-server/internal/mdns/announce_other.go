//go:build !windows

package mdns

// Only Windows has dnsapi.dll (announce_windows.go).
// The same file is in api/internal/discovery and cmd/maktaba-server/internal/mdns (two
// modules); tools/ci/mdns_copies.py checks they stay the same.

func dnsapiAvailable() bool { return false }

func systemDNSAPIImpl() dnsAPI { return nil }
