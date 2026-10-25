package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A node's connection to its home's database offers its cluster-link certificate on TLS
// (story 35.16): never on a plain one, nor without both files.
func TestHomeDSNOffersTheClientCertificate(t *testing.T) {
	dir := t.TempDir()
	dsn := "postgres://r:p@home:5432/maktaba?sslmode=verify-ca&sslrootcert=ca.crt"
	if got := withClientCert(dsn, dir); got != dsn {
		t.Fatalf("no certificate yet: %s", got)
	}
	for _, f := range []string{"cluster-node.crt", "cluster-node.key"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := withClientCert(dsn, dir)
	if !strings.Contains(got, "sslcert=") || !strings.Contains(got, "sslkey=") || !strings.Contains(got, "sslmode=verify-ca") {
		t.Fatalf("with a certificate: %s", got)
	}
	plain := "postgres://r:p@home:5432/maktaba?sslmode=disable"
	if got := withClientCert(plain, dir); got != plain {
		t.Fatalf("a plain connection: %s", got)
	}
}
