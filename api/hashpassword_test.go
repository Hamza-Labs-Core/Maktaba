package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/argon2id"
)

// hash-password (the desktop app's first-run setup): one line on stdin, its
// argon2id hash on stdout, which Verify accepts and adduser can store.
func TestHashPassword(t *testing.T) {
	var out bytes.Buffer
	if err := runHashPassword(strings.NewReader("s3cret pass\r\n"), &out); err != nil {
		t.Fatal(err)
	}
	hash := strings.TrimSpace(out.String())
	if err := argon2id.Valid(hash); err != nil {
		t.Fatalf("not a valid hash %q: %v", hash, err)
	}
	if err := argon2id.Verify("s3cret pass", hash); err != nil {
		t.Fatalf("the hash does not verify the password (the line end is not part of it): %v", err)
	}
	if strings.Contains(out.String(), "s3cret") {
		t.Fatal("the password must never be printed")
	}
	if err := runHashPassword(strings.NewReader("\n"), &out); err == nil {
		t.Error("an empty password must be refused")
	}
	if err := runHashPassword(strings.NewReader(strings.Repeat("x", 300)), &out); err == nil {
		t.Error("a password past the length cap must be refused")
	}
}
