//go:build !e2etrust

package pluginsig

// e2eRoot: a release build trusts only the embedded root (root_e2e.go is a test build's).
func e2eRoot() ([]byte, bool) { return nil, false }

func e2eFile(string) ([]byte, bool) { return nil, false }
