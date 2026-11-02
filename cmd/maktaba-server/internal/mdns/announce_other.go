//go:build !windows

package mdns

// Only Windows has dnsapi.dll (announce_windows.go).
// api/internal/discovery has the same file (another module).

func dnsapiAvailable() bool { return false }

func systemDNSAPIImpl() dnsAPI { return nil }
