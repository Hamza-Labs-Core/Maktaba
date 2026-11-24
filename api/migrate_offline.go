package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Offline migrations (research item 5, E3): the home's server runs an update's
// migrations before any of its roles start (cmd/maktaba-server's database step), with
// the library stopped, and says so with MAKTABA_MIGRATE_OFFLINE=1. Their index builds
// then run plainly, not CONCURRENTLY: a plain CREATE INDEX takes a SHARE lock (writes to
// that table wait, which nothing of the stopped library makes), waits for no older
// snapshot (a CONCURRENTLY build waits for every transaction older than it: on 2026-11-14
// and 2026-11-23 that was a machine's copy, for half an hour, until the lock timeout
// cancelled it), and rolls back whole instead of leaving an INVALID index. The files keep
// CONCURRENTLY (migration-lint rule 4): a migration may also run on a database that
// serves (a development database, a future online update), where it is needed.

// envMigrateOffline is the variable the server's database step sets.
const envMigrateOffline = "MAKTABA_MIGRATE_OFFLINE"

// migrateOffline reports whether this run of the migrations is offline.
func migrateOffline() bool { return strings.TrimSpace(os.Getenv(envMigrateOffline)) == "1" }

// concurrentlyStmt is a statement that builds or drops an index CONCURRENTLY, matched at
// the start of a statement: the keyword's span is cut.
var concurrentlyStmt = regexp.MustCompile(`(?is)^(?:CREATE\s+(?:UNIQUE\s+)?INDEX|DROP\s+INDEX)(\s+CONCURRENTLY)\b`)

// plainIndexBuilds rewrites the Up section of a migration's text: each statement that
// starts CREATE [UNIQUE] INDEX CONCURRENTLY or DROP INDEX CONCURRENTLY loses its
// CONCURRENTLY. Text in quotes, dollar quotes (a function's body, a DO block: its SQL
// runs later, maybe online) and comments is left as it is. It returns the text and how
// many statements it changed.
func plainIndexBuilds(text string) (string, int) {
	up, down := text, ""
	if loc := gooseDown.FindStringIndex(text); loc != nil {
		up, down = text[:loc[0]], text[loc[0]:]
	}
	var out strings.Builder
	n := 0
	stmtStart := true // the next code character begins a statement
	for i := 0; i < len(up); {
		c := up[i]
		switch {
		case c == '-' && i+1 < len(up) && up[i+1] == '-': // a line comment
			j := strings.IndexByte(up[i:], '\n')
			if j < 0 {
				j = len(up) - i
			}
			out.WriteString(up[i : i+j])
			i += j
			continue
		case c == '/' && i+1 < len(up) && up[i+1] == '*': // a block comment
			j := strings.Index(up[i+2:], "*/")
			end := len(up)
			if j >= 0 {
				end = i + 2 + j + 2
			}
			out.WriteString(up[i:end])
			i = end
			continue
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			out.WriteByte(c)
			i++
			continue
		}
		if stmtStart {
			stmtStart = false
			if m := concurrentlyStmt.FindStringSubmatchIndex(up[i:]); m != nil {
				out.WriteString(up[i : i+m[2]])
				i += m[3] // CONCURRENTLY dropped; the space after it stays
				n++
				continue
			}
		}
		switch {
		case c == ';':
			stmtStart = true
			out.WriteByte(c)
			i++
		case c == '\'': // a string ('' inside it is a quote)
			j := i + 1
			for j < len(up) {
				if up[j] == '\'' {
					if j+1 < len(up) && up[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			end := min(j+1, len(up))
			out.WriteString(up[i:end])
			i = end
		case c == '$': // a dollar quote: $$ or $tag$
			tag := dollarTag(up[i:])
			if tag == "" {
				out.WriteByte(c)
				i++
				continue
			}
			j := strings.Index(up[i+len(tag):], tag)
			end := len(up)
			if j >= 0 {
				end = i + len(tag) + j + len(tag)
			}
			out.WriteString(up[i:end])
			i = end
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String() + down, n
}

// dollarTagRe is a dollar quote's opening tag.
var dollarTagRe = regexp.MustCompile(`^\$[A-Za-z_]*\$`)

func dollarTag(s string) string { return dollarTagRe.FindString(s) }

// plainIndexBuildsIn rewrites every migration in the staged folder dir (plainIndexBuilds)
// and returns how many statements it changed.
func plainIndexBuildsIn(dir string) (int, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			return total, err
		}
		text, n := plainIndexBuilds(string(b))
		if n == 0 {
			continue
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			return total, fmt.Errorf("%s: %w", e.Name(), err)
		}
		total += n
	}
	return total, nil
}
