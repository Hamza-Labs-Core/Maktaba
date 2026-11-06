package testpg

import "testing"

// #644: the pipeline's form of the URL (no sslmode, a local server without SSL) works
// for lib/pq too; a URL that sets sslmode, and a remote server's, are left alone.
func TestNormalize(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"postgresql://postgres@127.0.0.1:55432/postgres", "postgresql://postgres@127.0.0.1:55432/postgres?sslmode=disable"},
		{"postgres://u:p@localhost/db?connect_timeout=5", "postgres://u:p@localhost/db?connect_timeout=5&sslmode=disable"},
		{"postgres://u@[::1]:5432/db", "postgres://u@[::1]:5432/db?sslmode=disable"},
		{"postgres://u@127.0.0.2/db", "postgres://u@127.0.0.2/db?sslmode=disable"},
		{"postgres:///db?host=/var/run/postgresql", "postgres:///db?host=%2Fvar%2Frun%2Fpostgresql&sslmode=disable"},
		{"postgres://u@127.0.0.1/db?sslmode=disable", "postgres://u@127.0.0.1/db?sslmode=disable"},
		{"postgres://u@127.0.0.1/db?sslmode=require", "postgres://u@127.0.0.1/db?sslmode=require"},
		{"postgres://u@db.example.com/db", "postgres://u@db.example.com/db"},
		{"postgres://u@10.0.0.5/db", "postgres://u@10.0.0.5/db"},
		{"host=127.0.0.1 user=postgres dbname=x", "host=127.0.0.1 user=postgres dbname=x sslmode=disable"},
		{"user=postgres dbname=x", "user=postgres dbname=x sslmode=disable"},
		{"host=localhost sslmode=prefer", "host=localhost sslmode=prefer"},
		{"host=db.example.com user=postgres", "host=db.example.com user=postgres"},
		{"", ""},
	} {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGet(t *testing.T) {
	t.Setenv(Env, "")
	if got := Get(); got != "" {
		t.Errorf("unset: %q", got)
	}
	t.Setenv(Env, "postgresql://postgres@127.0.0.1:55432/postgres")
	if got := Get(); got != "postgresql://postgres@127.0.0.1:55432/postgres?sslmode=disable" {
		t.Errorf("set: %q", got)
	}
}
