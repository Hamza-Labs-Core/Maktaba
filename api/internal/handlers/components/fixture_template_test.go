package components

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// The fixtures' databases are built once per test binary and copied for each test: built
// statement by statement in SQLite (each one a locked write of the file), the home took
// most of a test's time, over 1 s under -race on CI for a test that builds three.

var (
	templateDir  string
	templatesMu  sync.Mutex
	templateFile = map[string]string{}
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "components-fixtures-")
	if err != nil {
		panic(err)
	}
	templateDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// fromTemplate is a database of this test's own, a copy of the one build makes (built
// once, by the first test that asks for name).
func fromTemplate(t *testing.T, name string, build func(testing.TB, *sql.DB)) *sql.DB {
	t.Helper()
	templatesMu.Lock()
	src, ok := templateFile[name]
	if !ok {
		src = filepath.Join(templateDir, name+".db")
		db, err := sql.Open("sqlite", src)
		if err != nil {
			templatesMu.Unlock()
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		build(t, db)
		if err := db.Close(); err != nil {
			templatesMu.Unlock()
			t.Fatal(err)
		}
		templateFile[name] = src
	}
	templatesMu.Unlock()
	dst := filepath.Join(t.TempDir(), "c.db")
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dst)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
