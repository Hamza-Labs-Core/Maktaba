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
