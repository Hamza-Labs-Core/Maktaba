package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGooseCheck(t *testing.T) {
	const ok = "-- +goose Up\n-- +goose StatementBegin\nSELECT 1;\n-- +goose StatementEnd\n\n" +
		"-- +goose Down\n-- +goose StatementBegin\nSELECT 1;\n-- +goose StatementEnd\n"
	cases := []struct {
		name, body, want string // want "" = no violation
	}{
		{"well formed", ok, ""},
		{"no down section", "-- +goose Up\nSELECT 1;\n", ""},
		{"no transaction and plain statements", "-- +goose NO TRANSACTION\n-- +goose Up\nCREATE INDEX CONCURRENTLY IF NOT EXISTS i ON t (c);\n", ""},
		// The shape 0215_fp_roles had on PR #925: the header copy took the first block's
		// opening lines, so the block opened twice.
		{"a block opened twice",
			"-- +goose Up\n-- header\n-- +goose StatementBegin\n-- +goose StatementBegin\nDO $$ BEGIN END $$;\n-- +goose StatementEnd\n",
			"line 4: +goose StatementBegin inside the block opened at line 3"},
		{"begin before up", "-- +goose StatementBegin\nSELECT 1;\n-- +goose StatementEnd\n-- +goose Up\n",
			"line 1: +goose StatementBegin before +goose Up or +goose Down"},
		{"end without begin", "-- +goose Up\nSELECT 1;\n-- +goose StatementEnd\n",
			"line 3: +goose StatementEnd with no open +goose StatementBegin"},
		{"never closed", "-- +goose Up\n-- +goose StatementBegin\nSELECT 1;\n",
			"line 2: +goose StatementBegin never closed"},
		{"down inside a block", "-- +goose Up\n-- +goose StatementBegin\nSELECT 1;\n-- +goose Down\n-- +goose StatementEnd\n",
			"line 4: +goose Down inside the block opened at line 2"},
		{"down before up", "-- +goose Down\nSELECT 1;\n-- +goose Up\nSELECT 1;\n",
			"line 1: +goose Down before +goose Up"},
		{"two ups", "-- +goose Up\nSELECT 1;\n-- +goose Up\nSELECT 1;\n", "line 3: a second +goose Up"},
		{"no up", "SELECT 1;\n", "line 1: no +goose Up annotation"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vs := gooseCheck("x.sql", []byte(c.body))
			if c.want == "" {
				if len(vs) != 0 {
					t.Fatalf("want no violation, got %v", vs)
				}
				return
			}
			if len(vs) == 0 || !strings.HasPrefix(vs[0].Message, c.want) || vs[0].Rule != "goose-annotations" {
				t.Fatalf("want first violation %q, got %v", c.want, vs)
			}
		})
	}
}

// Every migration in the repository passes, so the check refuses only files goose refuses.
func TestGooseCheckPassesTheRepository(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "shared", "db", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found: %v", err)
	}
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if vs := gooseCheck(f, body); len(vs) != 0 {
			t.Errorf("%v", vs)
		}
	}
}
