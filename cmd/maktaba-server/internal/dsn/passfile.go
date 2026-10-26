package dsn

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The password of a Postgres URL never travels in the URL: a process the server starts
// gets the URL without it and PGPASSFILE naming a 0600 file that holds it (lib/pq, pgx,
// asyncpg and libpq's tools all read it when the URL has no password). A URL is printed
// by help texts, errors and logs; the file is read by the driver alone.

// SplitPassword takes the password out of a postgres:// (or postgresql://) URL: the URL
// without it, and the passfile line for it ("host:port:database:user:password"). A URL
// with no password, or another kind of DSN, comes back as it is with no line.
func SplitPassword(raw string) (bare, line string) {
	if !strings.HasPrefix(raw, "postgres://") && !strings.HasPrefix(raw, "postgresql://") {
		return raw, ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw, ""
	}
	pw, ok := u.User.Password()
	if !ok {
		return raw, ""
	}
	user := u.User.Username()
	u.User = url.User(user)
	field := func(v string) string {
		if v == "" {
			return "*"
		}
		// the passfile's escapes: a backslash and a colon
		return strings.NewReplacer(`\`, `\\`, `:`, `\:`).Replace(v)
	}
	host := u.Hostname()
	if host == "" {
		host = u.Query().Get("host")
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	return u.String(), strings.Join([]string{field(host), field(port),
		field(strings.TrimPrefix(u.Path, "/")), field(user), field(pw)}, ":")
}

// WritePassFile writes the lines to a 0600 file in dir named "pgpass-<name>" and returns
// its path. Callers name it by the connections it serves, never by a password (two
// processes started at once for the same connections write the same file).
func WritePassFile(dir, name string, lines []string) (string, error) {
	if len(lines) == 0 {
		return "", errors.New("dsn: no passfile lines")
	}
	sorted := append([]string(nil), lines...)
	sort.Strings(sorted)
	body := strings.Join(sorted, "\n") + "\n"
	path := filepath.Join(dir, "pgpass-"+name)
	if b, err := os.ReadFile(path); err == nil && string(b) == body {
		if st, err := os.Stat(path); err == nil && st.Mode().Perm() == 0o600 {
			return path, nil
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".pgpass-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

// Redact masks the credentials of a URL in s (for an error or a log line).
func Redact(s string) string { return redact(s) }

// ProcessEnv readies a child's environment: every *DATABASE_URL loses its password to a
// passfile in dir, and PGPASSFILE names that file. An environment with no password in
// it comes back as it is.
func ProcessEnv(env []string, dir string) ([]string, error) {
	var lines, bares []string
	seen := map[string]bool{}
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if ok && strings.HasSuffix(k, "DATABASE_URL") {
			bare, line := SplitPassword(v)
			if line != "" {
				e = k + "=" + bare
				if !seen[line] {
					seen[line] = true
					lines = append(lines, line)
					bares = append(bares, bare)
				}
			}
		}
		if ok && k == "PGPASSFILE" && len(lines) > 0 {
			continue
		}
		out = append(out, e)
	}
	if len(lines) == 0 {
		return out, nil
	}
	// named by the URLs without their passwords: no digest of a secret in a file name
	sort.Strings(bares)
	sum := sha256.Sum256([]byte(strings.Join(bares, "\n")))
	path, err := WritePassFile(dir, hex.EncodeToString(sum[:6]), lines)
	if err != nil {
		return nil, err
	}
	filtered := out[:0]
	for _, e := range out {
		if !strings.HasPrefix(e, "PGPASSFILE=") {
			filtered = append(filtered, e)
		}
	}
	return append(filtered, "PGPASSFILE="+path), nil
}
