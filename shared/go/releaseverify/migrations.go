package releaseverify

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	return walkMigrations(archive, func(base string, b []byte) error {
		return os.WriteFile(filepath.Join(dir, base), b, 0o644)
	})
}

// MigrationSet is the digest of the migrations a migrations archive holds (release.json's
// migrations.set_sha256): of each migration's name and the SHA-256 of its text, in name
// order, and of nothing else. Two releases with the same migrations have the same set
// whatever their archives' times and order (the archive's own digest changes with every
// release: its entries carry the release commit's time).
func MigrationSet(archive []byte) (string, error) {
	sums := map[string]string{}
	if _, err := walkMigrations(archive, func(base string, b []byte) error {
		if _, dup := sums[base]; dup {
			return fmt.Errorf("migrations archive: %s twice", base)
		}
		d := sha256.Sum256(b)
		sums[base] = hex.EncodeToString(d[:])
		return nil
	}); err != nil {
		return "", err
	}
	names := make([]string, 0, len(sums))
	for n := range sums {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		_, _ = fmt.Fprintf(h, "%s\x00%s\n", n, sums[n])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// walkMigrations gives each migration of a migrations archive to use (its base name and
// text), refusing anything else as ExtractMigrations says. It returns how many it gave.
func walkMigrations(archive []byte, use func(base string, b []byte) error) (int, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return 0, fmt.Errorf("migrations archive: %w", err)
	}
	defer func() { _ = gz.Close() }()
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
		if err := use(base, b); err != nil {
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
