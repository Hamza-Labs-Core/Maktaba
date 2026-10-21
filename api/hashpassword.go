package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/argon2id"
)

// runHashPassword implements `maktaba-api hash-password`: one line on stdin
// (the password, never in argv or the environment), its argon2id PHC hash on
// stdout, with the parameters every user's hash gets. The desktop app's
// first-run setup (Epic 38 story 38.6) hashes the admin's password when it is
// typed, so only the hash waits on disk until `adduser --password-hash-file`
// seeds the account; the hashing stays here, in one place.
func runHashPassword(in io.Reader, out io.Writer) error {
	p := argon2id.DefaultParams()
	line, err := bufio.NewReader(io.LimitReader(in, int64(p.MaxLen)+3)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read password: %w", err)
	}
	// a trailing newline is the line's end, not the password's (as adduser's
	// --password-file); other whitespace is kept
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		return errors.New("password: empty")
	}
	hash, err := argon2id.Hash(password, p)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, hash)
	return err
}
