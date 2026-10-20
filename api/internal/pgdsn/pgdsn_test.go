package pgdsn

import (
	"context"
	"errors"
	"testing"

	"github.com/lib/pq"
)

func TestForLibPQ(t *testing.T) {
	noSSL := func(context.Context, string) error { return pq.ErrSSLNotSupported }
	sslOK := func(context.Context, string) error { return nil }
	down := func(context.Context, string) error { return errors.New("dial tcp: connection refused") }
	never := func(context.Context, string) error { t.Fatal("probed"); return nil }

	const node = "postgres://node_1e943eda:pw@192.168.50.177:5432/maktaba?sslmode=prefer"
	for _, c := range []struct {
		name, dsn string
		probe     Probe
		want      string
	}{
		{"prefer, server without SSL", node, noSSL, "postgres://node_1e943eda:pw@192.168.50.177:5432/maktaba?sslmode=disable"},
		{"prefer, server with SSL", node, sslOK, "postgres://node_1e943eda:pw@192.168.50.177:5432/maktaba?sslmode=require"},
		{"prefer, server unreachable keeps SSL", node, down, "postgres://node_1e943eda:pw@192.168.50.177:5432/maktaba?sslmode=require"},
		{"allow", "postgresql://u@h/db?sslmode=allow", never, "postgresql://u@h/db?sslmode=disable"},
		{"key=value prefer", "host=h user=u sslmode=prefer dbname=db", noSSL, "host=h user=u sslmode=disable dbname=db"},
		{"supported mode unchanged", "postgres://u@h/db?sslmode=disable", never, "postgres://u@h/db?sslmode=disable"},
		{"no sslmode unchanged", "postgres://u@h/db", never, "postgres://u@h/db"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := ForLibPQ(context.Background(), c.dsn, c.probe); got != c.want {
				t.Fatalf("ForLibPQ(%q) = %q, want %q", c.dsn, got, c.want)
			}
		})
	}
}
