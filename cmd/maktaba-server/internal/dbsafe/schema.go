package dbsafe

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// DumpSchema is the schema a dump holds: the newest migration applied in it (goose's
// table), read from the dump itself without a database (pg_restore writes the table's
// rows as a script). A restore by hand runs the kept release whose schema this is
// (story 35.5 AC8). 0 with no error: the dump holds no migrations (an empty database).
func (t Target) DumpSchema(ctx context.Context, dump string) (int64, error) {
	out, err := exec.CommandContext(ctx, t.bin("pg_restore"), "--data-only", "--table=goose_db_version",
		"--file=-", dump).Output()
	if err != nil {
		msg := err.Error()
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			msg = tail(ee.Stderr)
		}
		return 0, fmt.Errorf("dump_unreadable: pg_restore %s: %s", dump, msg)
	}
	return appliedInScript(bytes.NewReader(out))
}

// appliedInScript reads the newest applied migration from a restore script's COPY of
// goose_db_version (version_id and is_applied by the COPY's column list).
func appliedInScript(r io.Reader) (int64, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	var (
		in              bool
		verCol, applCol = -1, -1
		top             int64
	)
	for sc.Scan() {
		line := sc.Text()
		if !in {
			// COPY public.goose_db_version (id, version_id, is_applied, tstamp) FROM stdin;
			rest, ok := strings.CutPrefix(line, "COPY ")
			if !ok || !strings.Contains(rest, "goose_db_version") {
				continue
			}
			open, cl := strings.Index(rest, "("), strings.Index(rest, ")")
			if open < 0 || cl < open {
				return 0, fmt.Errorf("dump_unreadable: goose_db_version's COPY names no columns: %q", line)
			}
			for i, c := range strings.Split(rest[open+1:cl], ",") {
				switch strings.Trim(strings.TrimSpace(c), `"`) {
				case "version_id":
					verCol = i
				case "is_applied":
					applCol = i
				}
			}
			if verCol < 0 {
				return 0, fmt.Errorf("dump_unreadable: goose_db_version's COPY has no version_id: %q", line)
			}
			in = true
			continue
		}
		if line == `\.` {
			return top, nil
		}
		f := strings.Split(line, "\t")
		if verCol >= len(f) || (applCol >= 0 && applCol < len(f) && f[applCol] != "t") {
			continue
		}
		if v, err := strconv.ParseInt(f[verCol], 10, 64); err == nil && v > top {
			top = v
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return top, nil // no goose table in the dump: no migrations
}
