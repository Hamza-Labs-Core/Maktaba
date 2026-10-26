package auth

import (
	"testing"
)

// A viewer's web session reads the libraries the viewer is granted, as a
// token does: before, the session's principal had none, and the web showed a
// viewer no library at all. A grant made after the sign-in counts at once.
func TestCookieSessionCarriesLibraryGrants(t *testing.T) {
	c := newCloudHome(t)
	viewer := c.user("vic", false)
	if _, err := c.db.Exec(`INSERT INTO library_acl (user_id, library_id) VALUES ($1, $2)`, viewer.ID, c.libs[0]); err != nil {
		t.Fatal(err)
	}
	web := c.do("POST", "/api/auth/login", "", map[string]string{"username": "vic", "password": "correct horse battery"})
	var cookie string
	for _, ck := range web.resp.Cookies() {
		if ck.Name == CookieSession {
			cookie = "mkt_sess=" + ck.Value
		}
	}
	if web.code != 200 || cookie == "" {
		t.Fatalf("web sign-in: %d %v", web.code, web.body)
	}
	libs := func() []any {
		me := c.do("GET", "/api/auth/me", cookie, nil)
		if me.code != 200 {
			t.Fatalf("me: %d %v", me.code, me.body)
		}
		l, _ := me.body["libraries"].([]any)
		return l
	}
	if got := libs(); len(got) != 1 || got[0] != c.libs[0] {
		t.Fatalf("session libraries = %v, want [%s]", got, c.libs[0])
	}
	if _, err := c.db.Exec(`INSERT INTO library_acl (user_id, library_id) VALUES ($1, $2)`, viewer.ID, c.libs[1]); err != nil {
		t.Fatal(err)
	}
	if got := libs(); len(got) != 2 {
		t.Fatalf("after a second grant the session reads %v, want both libraries", got)
	}
}
