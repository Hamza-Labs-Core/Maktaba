package dsn

import "testing"

func TestClassify(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		in          string
		wantBackend Backend
		wantDriver  string
		wantDialect string
		wantPath    string
	}{
		{"postgres", "postgres://u:p@localhost:5432/maktaba?sslmode=disable", Postgres, "postgres", "postgres", ""},
		{"postgresql", "postgresql://localhost/maktaba", Postgres, "postgres", "postgres", ""},
		{"sqlite triple slash absolute", "sqlite:///var/lib/maktaba/maktaba.db", SQLite, "sqlite", "sqlite3", "/var/lib/maktaba/maktaba.db"},
		{"sqlite relative double slash", "sqlite://./maktaba.db", SQLite, "sqlite", "sqlite3", "./maktaba.db"},
		{"sqlite short", "sqlite:maktaba.db", SQLite, "sqlite", "sqlite3", "maktaba.db"},
		{"sqlite with pragma", "sqlite:///tmp/m.db?_pragma=busy_timeout(5000)", SQLite, "sqlite", "sqlite3", "/tmp/m.db"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Classify(tc.in)
			if err != nil {
				t.Fatalf("Classify(%q) error: %v", tc.in, err)
			}
			if got.Backend != tc.wantBackend {
				t.Errorf("backend = %v, want %v", got.Backend, tc.wantBackend)
			}
			if got.Driver != tc.wantDriver {
				t.Errorf("driver = %q, want %q", got.Driver, tc.wantDriver)
			}
			if got.Dialect != tc.wantDialect {
				t.Errorf("dialect = %q, want %q", got.Dialect, tc.wantDialect)
			}
			if got.SQLitePath != tc.wantPath {
				t.Errorf("sqlitePath = %q, want %q", got.SQLitePath, tc.wantPath)
			}
		})
	}
}

func TestClassifyRejectsUnknown(t *testing.T) {
	t.Parallel()
	if _, err := Classify("mysql://localhost/db"); err == nil {
		t.Fatal("expected error for unknown scheme")
	}
	if _, err := Classify(""); err == nil {
		t.Fatal("expected error for empty DSN")
	}
}

func TestClassifyRedactsPasswordInError(t *testing.T) {
	t.Parallel()
	_, err := Classify("mysql://user:secret@host/db")
	if err == nil {
		t.Fatal("expected error")
	}
	if contains(err.Error(), "secret") {
		t.Errorf("error leaked password: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
