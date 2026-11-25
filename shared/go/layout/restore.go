package layout

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// RestoreProtocol is what this build's restore can do: 1, put a backup back inside the public
// schema, on a Postgres the home does not bundle whose database user may not make that schema
// and its extensions again (cmd/maktaba-server/internal/dbsafe/inside.go). A release says it in
// its signed release.json (restore_protocol: tools/release/genrelease reads this line), and in
// its migrations' RestoreFile where it has no release.json (an app's payload): a backup that
// needs it is not put back on a release that does not say it.
const RestoreProtocol = 1

// RestoreFile is the file in a release's migrations folder that says it.
const RestoreFile = "meta/restore.json"

// ReleaseRestoreProtocol is the restore protocol of the release in dir (its release.json, else
// its migrations' RestoreFile), 0 when it says none or cannot be read.
func ReleaseRestoreProtocol(dir string) int {
	read := func(path, field string) int {
		b, err := os.ReadFile(path)
		if err != nil {
			return 0
		}
		m := map[string]any{}
		if json.Unmarshal(b, &m) != nil {
			return 0
		}
		n, _ := m[field].(float64)
		return int(n)
	}
	return max(read(filepath.Join(dir, "release.json"), "restore_protocol"),
		read(filepath.Join(dir, "migrations", filepath.FromSlash(RestoreFile)), "protocol"))
}
