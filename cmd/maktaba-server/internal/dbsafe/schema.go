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

// appliedInScript reads the schema a restore script's COPY of goose_db_version holds: the
// newest migration still applied (version_id and is_applied by the COPY's column list).
// goose records a migration run down as a later row with is_applied false, so each
// version counts as its latest row (by id, else by order) says, as goose itself reads it.
func appliedInScript(r io.Reader) (int64, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	type row struct {
		id      int64
		applied bool
	}
	var (
		in                     bool
		idCol, verCol, applCol = -1, -1, -1
		latest                 = map[int64]row{}
		n                      int64
	)
	effective := func() int64 {
		var top int64
		for v, r := range latest {
			if r.applied && v > top {
				top = v
			}
		}
		return top
	}
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
				case "id":
					idCol = i
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
			return effective(), nil
		}
		f := strings.Split(line, "\t")
		if verCol >= len(f) {
			continue
		}
		v, err := strconv.ParseInt(f[verCol], 10, 64)
		if err != nil {
			continue
		}
		n++
		id := n // the row's order when the COPY names no id
		if idCol >= 0 && idCol < len(f) {
			if x, err := strconv.ParseInt(f[idCol], 10, 64); err == nil {
				id = x
			}
		}
		applied := applCol < 0 || (applCol < len(f) && f[applCol] == "t")
		if old, seen := latest[v]; !seen || id >= old.id {
			latest[v] = row{id: id, applied: applied}
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return effective(), nil // no goose table in the dump: no migrations
}
