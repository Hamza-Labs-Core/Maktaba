package db

import "testing"

func TestWithConnectTimeout(t *testing.T) {
	for in, want := range map[string]string{
		"postgres://u@h/d":                   "postgres://u@h/d?connect_timeout=2",
		"postgres://u@h/d?sslmode=disable":   "postgres://u@h/d?sslmode=disable&connect_timeout=2",
		"postgres://u@h/d?connect_timeout=9": "postgres://u@h/d?connect_timeout=9",
		"host=h dbname=d":                    "host=h dbname=d",
		"postgresql://u@h/d":                 "postgresql://u@h/d?connect_timeout=2",
	} {
		if got := WithConnectTimeout(in, 2); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// Story 50.18: one round trip per query, so PgBouncer's transaction pool can't split a
// query's Parse from its Bind; a URL that says otherwise keeps its choice.
func TestWithBinaryParameters(t *testing.T) {
	for in, want := range map[string]string{
		"postgres://u@h/d":                      "postgres://u@h/d?binary_parameters=yes",
		"postgres://u@h:6432/d?sslmode=require": "postgres://u@h:6432/d?sslmode=require&binary_parameters=yes",
		"postgres://u@h/d?binary_parameters=no": "postgres://u@h/d?binary_parameters=no",
		"host=h dbname=d":                       "host=h dbname=d",
		"postgresql://u@h/d?connect_timeout=2":  "postgresql://u@h/d?connect_timeout=2&binary_parameters=yes",
	} {
		if got := WithBinaryParameters(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
