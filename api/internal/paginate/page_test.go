package paginate

import (
	"testing"
	"time"
)

type row struct {
	id  string
	upd time.Time
}

func (r row) PageCursor() Cursor {
	return Cursor{Updated: r.upd, ID: r.id}
}

func TestBoundUnderLimit(t *testing.T) {
	items := []row{{id: "a", upd: time.Now()}, {id: "b", upd: time.Now()}}
	page := Bound(items, 5)
	if len(page.Items) != 2 || page.Next != nil {
		t.Fatalf("page = %+v, want 2 items and no next", page)
	}
}

func TestBoundExactlyLimit(t *testing.T) {
	items := []row{{id: "a", upd: time.Now()}, {id: "b", upd: time.Now()}}
	page := Bound(items, 2)
	if len(page.Items) != 2 || page.Next != nil {
		t.Fatalf("page = %+v, want 2 items and no next (LIMIT n exactly returns no next)", page)
	}
}

func TestBoundOverLimit(t *testing.T) {
	items := []row{
		{id: "a", upd: time.Unix(3, 0).UTC()},
		{id: "b", upd: time.Unix(2, 0).UTC()},
		{id: "c", upd: time.Unix(1, 0).UTC()}, // sentinel; trimmed
	}
	page := Bound(items, 2)
	if len(page.Items) != 2 {
		t.Fatalf("len = %d, want 2", len(page.Items))
	}
	if page.Next == nil {
		t.Fatal("expected next cursor for over-limit page")
	}
	dec, perr := Decode(*page.Next)
	if perr != nil {
		t.Fatalf("decode next: %+v", perr)
	}
	if dec.ID != "b" {
		t.Fatalf("next cursor id = %s, want b (last surviving row)", dec.ID)
	}
}
