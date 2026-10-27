//go:build integration && !windows

package pgbundle

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/storage"
)

// TestRealStorageMove moves a real cluster (a scratch one in a temp dir) to
// another folder as `maktaba-server storage move` does: the copy is verified by
// starting Postgres on it, and a table written before the move is read from it.
func TestRealStorageMove(t *testing.T) {
	home := realHome(t)
	if os.Geteuid() == 0 {
		t.Skip("postgres refuses to run as root")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	base, err := os.MkdirTemp("", "mkmv") // short: the unix socket path is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	from := filepath.Join(base, "a")
	keys := filepath.Join(base, "keys")
	b := Bundle{Home: home, Data: filepath.Join(from, "postgres"), Keys: keys, Backups: filepath.Join(from, "backups"), Port: port}
	ctx := context.Background()
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := b.WithServer(ctx, io.Discard, func() error {
		q, err := b.NewQuerier()
		if err != nil {
			return err
		}
		return q.Exec(ctx, AppDatabase, "CREATE TABLE moved (v text); INSERT INTO moved VALUES ('before the move');")
	}); err != nil {
		t.Fatal(err)
	}
	if b.Running() {
		t.Fatal("the cluster still runs")
	}
	cfgDir := filepath.Join(base, "cfg")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "server.toml"), []byte("[server]\ndata_dir = '"+from+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := storage.Install{ConfigDir: cfgDir, ConfigPath: filepath.Join(cfgDir, "server.toml"),
		From:     storage.Layout{Data: from, Root: from},
		ToLayout: func(t string) storage.Layout { return storage.Layout{Data: t, Root: t} }}
	target := filepath.Join(base, "b")
	verified := false
	_, err = storage.Move(ctx, in, target, storage.Options{
		Busy: func(l storage.Layout) error { return nil },
		VerifyPostgres: func(ctx context.Context, root string) error {
			c := b
			c.Data = filepath.Join(root, "postgres")
			verified = true
			return c.Verify(ctx, io.Discard)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !verified {
		t.Fatal("the copy was not started")
	}
	moved := b
	moved.Data = filepath.Join(target, "postgres")
	var rows []string
	if err := moved.WithServer(ctx, io.Discard, func() error {
		q, err := moved.NewQuerier()
		if err != nil {
			return err
		}
		rows, err = q.Query(ctx, AppDatabase, "SELECT v FROM moved")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0] != "before the move" {
		t.Fatalf("rows %v", rows)
	}
}
