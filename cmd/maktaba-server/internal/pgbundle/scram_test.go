package pgbundle

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
)

// RFC 7677's test vector (user "user", password "pencil"): the StoredKey and ServerKey
// Postgres keeps for it.
func TestScramVerifierMatchesRFC7677(t *testing.T) {
	salt, err := base64.StdEncoding.DecodeString("W22ZaJ0SNY7soEsUEjb6gQ==")
	if err != nil {
		t.Fatal(err)
	}
	got, err := scramVerifierWith("pencil", salt, 4096)
	if err != nil {
		t.Fatal(err)
	}
	want := "SCRAM-SHA-256$4096:W22ZaJ0SNY7soEsUEjb6gQ==$WG5d8oPm3OtcPnkdi4Uo7BkeZkBFzpcXkuLmtbsT4qY=:" +
		"wfPLwcE6nTWhTAmQ7tl2KeoiWGPlZqQxSrmfPwDl2dU="
	if got != want {
		t.Fatalf("verifier = %s\nwant      %s", got, want)
	}
}

// Each secret has a salt of its own, and never the password in it.
func TestScramVerifierIsSaltedAndHidesThePassword(t *testing.T) {
	a, err := scramVerifier("s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	b, err := scramVerifier("s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two secrets of one password share their salt")
	}
	if strings.Contains(a, "s3cret-pass") || !strings.HasPrefix(a, "SCRAM-SHA-256$4096:") {
		t.Errorf("verifier %q", a)
	}
	if !setsPassword(t, bootstrapSQL(a), "s3cret-pass") {
		t.Error("bootstrapSQL does not set the secret")
	}
}

var passwordRe = regexp.MustCompile(`PASSWORD 'SCRAM-SHA-256\$(\d+):([A-Za-z0-9+/=]+)\$[^']*'`)

// setsPassword reports whether sql sets a role's password to pw's SCRAM secret.
func setsPassword(t *testing.T, sql, pw string) bool {
	t.Helper()
	m := passwordRe.FindStringSubmatch(sql)
	if m == nil {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(m[2])
	if err != nil || m[1] != "4096" {
		return false
	}
	want, err := scramVerifierWith(pw, salt, 4096)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(sql, "PASSWORD '"+want+"'")
}
