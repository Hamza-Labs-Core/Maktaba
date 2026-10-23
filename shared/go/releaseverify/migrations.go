package releaseverify

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// migrationName is a migration file a release carries: shared/db/migrations'
// "<slot>_<name>.sql" (Postgres) and "<slot>_<name>.sqlite.sql" (SQLite).
var migrationName = regexp.MustCompile(`^[0-9]{4,6}_[a-z0-9_]+(\.sqlite)?\.sql$`)

// maxMigrations bounds the migrations archive and what it unpacks to.
const maxMigrations = 64 << 20

// ExtractMigrations unpacks a verified migrations archive (release.json's
// migrations, a .tar.gz) into dir, which it makes: every regular file whose base
// name is a migration's, flat (a "migrations/" prefix in the archive is dropped).
// Anything else (a link, a path that climbs, another name) is refused, so a
// directory it made holds migrations only. It returns how many it wrote.
func ExtractMigrations(archive []byte, dir string) (int, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return 0, fmt.Errorf("migrations archive: %w", err)
	}
	defer func() { _ = gz.Close() }()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	tr := tar.NewReader(gz)
	var total int64
	n := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return n, fmt.Errorf("migrations archive: %w", err)
		}
		name := strings.TrimPrefix(path.Clean(strings.ReplaceAll(hdr.Name, "\\", "/")), "./")
		switch hdr.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return n, fmt.Errorf("migrations archive: %s is not a regular file", hdr.Name)
		}
		if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
			return n, fmt.Errorf("migrations archive: %s climbs out", hdr.Name)
		}
		base := path.Base(name)
		if dirPart := path.Dir(name); dirPart != "." && dirPart != "migrations" {
			return n, fmt.Errorf("migrations archive: %s is not a migration", hdr.Name)
		}
		if !migrationName.MatchString(base) {
			if base == "MANIFEST.md" || base == "README.md" {
				continue
			}
			return n, fmt.Errorf("migrations archive: %s is not a migration", hdr.Name)
		}
		total += hdr.Size
		if total > maxMigrations {
			return n, fmt.Errorf("migrations archive: larger than %d bytes unpacked", maxMigrations)
		}
		b, err := readCapped(tr, maxMigrations)
		if err != nil {
			return n, err
		}
		if err := os.WriteFile(filepath.Join(dir, base), b, 0o644); err != nil {
			return n, err
		}
		n++
	}
	if n == 0 {
		return 0, errors.New("migrations archive: no migrations in it")
	}
	return n, nil
}

// FetchMigrations downloads the verified release's migrations archive and checks
// it against checksums.txt; nil (no error) when the release carries none.
func FetchMigrations(ctxGet func(url string, limit int64) ([]byte, error), res *Result, assets map[string]string) ([]byte, error) {
	if res == nil || res.Manifest == nil || res.Manifest.Migrations == nil {
		return nil, nil
	}
	name := res.Manifest.Migrations.Archive
	u, ok := assets[name]
	if !ok {
		return nil, fail(ErrManifest, name, "listed in release.json but not attached to the release", ErrNotFound)
	}
	b, err := ctxGet(u, maxMigrations)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", name, err)
	}
	if err := res.VerifyArtifact(Artifact{Name: name, Data: b}); err != nil {
		return nil, err
	}
	return b, nil
}
