package libraries

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// The merge on Postgres: the pins of the merged library move to the target,
// and a change to either library made while the merge runs is not lost (the
// merge reads both rows under a lock). Runs only with MAKTABA_TEST_PG_URL.
func TestMergePostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	const u1, u2 = "00000000-0000-4000-8000-0000000000e1", "00000000-0000-4000-8000-0000000000e2"
	for _, q := range []string{
		`INSERT INTO users (id, username, pw_hash) VALUES ('` + u1 + `', 'merge-u1', 'x'), ('` + u2 + `', 'merge-u2', 'x')`,
		`INSERT INTO libraries (id, name, roots) VALUES ('` + libBig + `', 'Big', '{/m/a}'), ('` + libSmall + `', 'Small', '{/m/b}')`,
		// u1 pinned the merged library alone, u2 both
		`INSERT INTO user_library_pins (user_id, library_id, position) VALUES
			('` + u1 + `', '` + libSmall + `', 3), ('` + u2 + `', '` + libSmall + `', 0), ('` + u2 + `', '` + libBig + `', 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	// another request holds the target's row: a rename under way
	other, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Rollback() }()
	if _, err := other.Exec(`UPDATE libraries SET name = 'Big renamed' WHERE id = $1`, libBig); err != nil {
		t.Fatal(err)
	}
	h := &Handler{DB: db, PathChecker: fakeChecker{}}
	done := make(chan *http.Response, 1)
	go func() {
		rec := call(t, h, "POST", "/api/libraries/"+libSmall+"/merge", `{"into":"`+libBig+`","confirm":"Small"}`)
		done <- rec.Result()
	}()
	select {
	case <-done:
		t.Fatal("the merge did not wait for the rename")
	case <-time.After(300 * time.Millisecond):
	}
	if err := other.Commit(); err != nil {
		t.Fatal(err)
	}
	var res *http.Response
	select {
	case res = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the merge did not finish")
	}
	var resp MergeResponse
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("merge: %d %v", res.StatusCode, err)
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM libraries WHERE id = $1`, libBig).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Big renamed" || resp.Into.Name != "Big renamed" {
		t.Errorf("name = %q, response %q: the rename was lost", name, resp.Into.Name)
	}

	pins := map[string]int{}
	rows, err := db.Query(`SELECT user_id::text, library_id::text, position FROM user_library_pins`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var u, l string
		var pos int
		if err := rows.Scan(&u, &l, &pos); err != nil {
			t.Fatal(err)
		}
		if l != libBig {
			t.Errorf("a pin of %s is left", l)
		}
		pins[u] = pos
	}
	if len(pins) != 2 || pins[u1] != 3 || pins[u2] != 1 {
		t.Errorf("pins = %v, want u1 at 3 and u2 at 1", pins)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM libraries WHERE id = $1`, libSmall).Scan(&n); err != nil || n != 0 {
		t.Errorf("the merged library is still there (%d, %v)", n, err)
	}
}
