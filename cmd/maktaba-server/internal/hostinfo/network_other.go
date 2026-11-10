//go:build !linux

package hostinfo

// ifaceDriver: only Linux runs Maktaba's container (network_linux.go).
func ifaceDriver(string) string { return "" }
