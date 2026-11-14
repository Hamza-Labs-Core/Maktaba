package dbtest

import "testing"

// A session URL keeps the scratch database and takes the session pool's server
// (story 50.18): unset, the URL is unchanged.
func TestSessionURL(t *testing.T) {
	for _, c := range []struct{ db, base, want string }{
		{"postgres://u:p@127.0.0.1:6432/cloud_t_1?sslmode=disable", "",
			"postgres://u:p@127.0.0.1:6432/cloud_t_1?sslmode=disable"},
		{"postgres://u:p@127.0.0.1:6432/cloud_t_1?sslmode=disable", "postgres://s:q@127.0.0.1:6433/maktaba?sslmode=disable",
			"postgres://s:q@127.0.0.1:6433/cloud_t_1?sslmode=disable"},
	} {
		got, err := sessionURL(c.db, c.base)
		if err != nil || got != c.want {
			t.Errorf("sessionURL(%q, %q) = %q, %v; want %q", c.db, c.base, got, err, c.want)
		}
	}
	if _, err := sessionURL("postgres://h/db", "postgres://%zz"); err == nil {
		t.Error("a bad session URL was accepted")
	}
}
