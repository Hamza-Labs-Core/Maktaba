package releaseverify

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
	"time"
)

// migrationsTarGz is a migrations archive of files, in that order, each entry dated at.
func migrationsTarGz(t *testing.T, at time.Time, files ...[2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.ModTime = at
	tw := tar.NewWriter(gz)
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f[0], Mode: 0o644, Size: int64(len(f[1])), ModTime: at,
			Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The set of a release's migrations is the same for two releases that carry the same
// migrations, whatever their archives' times and order (the archives' own digests differ:
// the release workflow dates each entry at the release commit), and differs when one
// migration's text does, or one is added (2026-11-24: v0.3.156 to v0.3.158 each changed
// 0235, the slot that rolled back).
func TestMigrationSet(t *testing.T) {
	t.Parallel()
	a, b := [2]string{"0234_docs.sql", "ALTER TABLE a;"}, [2]string{"0235_terms.sql", "CREATE INDEX CONCURRENTLY x;"}
	one := migrationsTarGz(t, time.Unix(1_800_000_000, 0), a, b)
	two := migrationsTarGz(t, time.Unix(1_800_090_000, 0), b, a)
	if bytes.Equal(one, two) {
		t.Fatal("the two archives are the same bytes: the test proves nothing")
	}
	s1, err := MigrationSet(one)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := MigrationSet(two)
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s2 || !hexRe.MatchString(s1) {
		t.Fatalf("the same migrations: sets %q and %q", s1, s2)
	}
	changed, err := MigrationSet(migrationsTarGz(t, time.Unix(1_800_000_000, 0), a,
		[2]string{"0235_terms.sql", "SET lock_timeout = 0; CREATE INDEX CONCURRENTLY x;"}))
	if err != nil {
		t.Fatal(err)
	}
	added, err := MigrationSet(migrationsTarGz(t, time.Unix(1_800_000_000, 0), a, b,
		[2]string{"0236_runs.sql", "ALTER TABLE r;"}))
	if err != nil {
		t.Fatal(err)
	}
	if changed == s1 || added == s1 || changed == added {
		t.Fatalf("a changed or added migration kept the set: %q %q %q", s1, changed, added)
	}
	// a README in the archive is not a migration: it does not change the set
	withReadme, err := MigrationSet(migrationsTarGz(t, time.Unix(1_800_000_000, 0), a, b,
		[2]string{"README.md", "notes"}))
	if err != nil {
		t.Fatal(err)
	}
	if withReadme != s1 {
		t.Fatalf("a README changed the set: %q, want %q", withReadme, s1)
	}
	if _, err := MigrationSet([]byte("not a tarball")); err == nil {
		t.Fatal("a broken archive has a set")
	}
	if _, err := MigrationSet(migrationsTarGz(t, time.Unix(1_800_000_000, 0), a, a)); err == nil {
		t.Fatal("an archive with one migration twice has a set")
	}
}
