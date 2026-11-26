package search

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestNormalisedLinesPostgresBudgetFallback(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  []int64
	}{
		{"query", []int64{1}},
		{"scan", []int64{1}},
		{"partial", []int64{1, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &passageTestConn{stall: tc.name}
			db := sql.OpenDB(conn)
			defer db.Close()
			h := &Handler{DB: db}
			hits, counts, marks, ok, err := h.normalisedLines(context.Background(), "test", []string{"video"}, 10)
			if err != nil || !ok || !conn.stalled || counts["video"] != len(tc.ids) {
				t.Fatalf("hits=%v counts=%v marks=%v ok=%v err=%v stalled=%v", hits, counts, marks, ok, err, conn.stalled)
			}
			var got []int64
			for _, hit := range hits["video"] {
				got = append(got, hit.SegmentID)
			}
			wantMarks := map[int64][]string{1: {"test"}}
			if tc.name == "partial" {
				wantMarks[2] = []string{"testing"}
			}
			if !reflect.DeepEqual(got, tc.ids) || !reflect.DeepEqual(marks, wantMarks) {
				t.Fatalf("IDs=%v marks=%v, want %v and %v", got, marks, tc.ids, wantMarks)
			}
		})
	}
}

type passageTestConn struct {
	stall   string
	stalled bool
}

func (c *passageTestConn) Connect(context.Context) (driver.Conn, error) { return c, nil }
func (c *passageTestConn) Driver() driver.Driver                        { return nil }
func (c *passageTestConn) Close() error                                 { return nil }
func (c *passageTestConn) Prepare(string) (driver.Stmt, error)          { return nil, driver.ErrSkip }
func (c *passageTestConn) Begin() (driver.Tx, error)                    { return nil, driver.ErrSkip }

func (c *passageTestConn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "SELECT tsvector_to_array"):
		return &passageTestRows{width: 2, values: [][]driver.Value{{"{test}", "{test}"}}}, nil
	case strings.Contains(query, "FROM transcripts"):
		return &passageTestRows{width: 5, values: [][]driver.Value{
			{int64(1), "video", 0.0, 1.0, "test"},
			{int64(2), "video", 1.0, 2.0, "testing"},
			{int64(3), "video", 2.0, 3.0, "tested"},
		}}, nil
	case strings.Contains(query, "FROM q, transcript_segments"):
		wait := func() error {
			<-ctx.Done()
			c.stalled = true
			return ctx.Err()
		}
		switch c.stall {
		case "query":
			return nil, wait()
		case "scan":
			return &passageTestRows{width: 3, after: func(dest []driver.Value) error {
				_ = wait()
				// Next delivers a row, so Scan observes either cancellation or the bad ID.
				copy(dest, []driver.Value{"invalid ID", 0.1, "{testing}"})
				return nil
			}}, nil
		case "partial":
			return &passageTestRows{width: 3, values: [][]driver.Value{{int64(2), 0.1, "{testing}"}},
				after: func([]driver.Value) error { return wait() }}, nil
		}
	}
	return nil, errors.New("unexpected passage query")
}

type passageTestRows struct {
	width  int
	values [][]driver.Value
	after  func([]driver.Value) error
}

func (r *passageTestRows) Columns() []string { return make([]string, r.width) }
func (r *passageTestRows) Close() error      { return nil }

func (r *passageTestRows) Next(dest []driver.Value) error {
	if len(r.values) > 0 {
		copy(dest, r.values[0])
		r.values = r.values[1:]
		return nil
	}
	if r.after != nil {
		return r.after(dest)
	}
	return io.EOF
}
