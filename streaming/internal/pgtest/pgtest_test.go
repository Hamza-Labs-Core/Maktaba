package pgtest

import "testing"

// #644: the pipeline's form of the URL (a local server without SSL, no sslmode) reaches
// the streaming tests, and their `migrate` run on lib/pq, with sslmode=disable; a URL
// that sets sslmode is passed on as it is.
func TestBaseIsTheURLLibPQTakes(t *testing.T) {
	t.Setenv("MAKTABA_TEST_PG_URL", "postgresql://postgres@127.0.0.1:55432/postgres")
	if got, want := Base(t), "postgresql://postgres@127.0.0.1:55432/postgres?sslmode=disable"; got != want {
		t.Errorf("Base = %q, want %q", got, want)
	}
	t.Setenv("MAKTABA_TEST_PG_URL", "postgres://u@127.0.0.1/db?sslmode=require")
	if got, want := Base(t), "postgres://u@127.0.0.1/db?sslmode=require"; got != want {
		t.Errorf("Base = %q, want %q", got, want)
	}
	if got, want := dsnFor(t, Base(t), "e32_x"), "postgres://u@127.0.0.1/e32_x?sslmode=require"; got != want {
		t.Errorf("dsnFor = %q, want %q", got, want)
	}
}
