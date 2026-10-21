package pgbundle

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// BackupAs writes a pg_dump custom-format dump of the app database into
// backups/<name>.dump (beside and renamed into place) and returns its path.
// Unlike Backup's nightly dumps it is never pruned: the desktop app takes one
// before an update's migrations (pre-<old>-to-<new>, Epic 38 story 38.12), the
// way back to the old version. The server must be up.
func (b Bundle) BackupAs(ctx context.Context, name string) (string, error) {
	if strings.ContainsAny(name, `/\`) || name == "" || strings.HasPrefix(name, backupPrefix) {
		return "", fmt.Errorf("backup name %q", name)
	}
	superPW, err := readPassword(b.passwordFile(SuperUser))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(b.Backups, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(b.Backups, name+backupSuffix)
	part := path + ".partial"
	cmd := exec.CommandContext(ctx, b.Bin("pg_dump"), "-Fc", "-h", "127.0.0.1", "-p", strconv.Itoa(b.Port),
		"-U", SuperUser, "-f", part, AppDatabase)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+superPW)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(part)
		return "", fmt.Errorf("pg_dump: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return path, os.Rename(part, path)
}

// ClusterMajor is the Postgres major version that made the cluster (its
// PG_VERSION file), "" before initdb.
func (b Bundle) ClusterMajor() (string, error) {
	raw, err := os.ReadFile(filepath.Join(b.Data, "PG_VERSION"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}
