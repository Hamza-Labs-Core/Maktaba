//go:build !windows

package discovery

// Only Windows has dnsapi.dll (announce_windows.go).
// cmd/maktaba-server/internal/mdns has the same file (another module).

func dnsapiAvailable() bool { return false }

func systemDNSAPIImpl() dnsAPI { return nil }
