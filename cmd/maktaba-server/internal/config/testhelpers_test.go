package config

import "os"

// writeFile is a tiny test helper so the partial-config test can drop a
// fixture without importing os everywhere.
func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}
