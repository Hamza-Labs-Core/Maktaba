package privacy

import (
	"context"
	"errors"
	"testing"
)

type fakeEraser struct {
	calls []string
	fail  map[string]bool
}

func (f *fakeEraser) Delete(_ context.Context, id string) (DeletionReport, error) {
	f.calls = append(f.calls, id)
	if f.fail[id] {
		return DeletionReport{UserID: id}, errors.New("db down")
	}
	return DeletionReport{UserID: id, PushRows: 2}, nil
}

// Stub audit #22: the purge erases each account's push logs (GDPR, story
// 30.2) before it drops the user, and never drops a user whose erasure
// failed.
func TestPurgeAccountsErasesBeforeDelete(t *testing.T) {
	er := &fakeEraser{fail: map[string]bool{"u2": true}}
	var order []string
	deleted := map[string]bool{}
	res := purgeAccounts(context.Background(), []string{"u1", "u2", "u3"}, er,
		func(_ context.Context, id string) error {
			if len(er.calls) == 0 || er.calls[len(er.calls)-1] != id {
				t.Fatalf("user %s deleted before its erasure (erasures %v)", id, er.calls)
			}
			order = append(order, id)
			deleted[id] = true
			return nil
		})
	if res.Purged != 2 || res.PushRows != 4 {
		t.Fatalf("result: %+v", res)
	}
	if deleted["u2"] || res.Failed["u2"] == nil {
		t.Fatalf("u2 erasure failed but the user was deleted: %+v", res)
	}
	if len(order) != 2 || order[0] != "u1" || order[1] != "u3" {
		t.Fatalf("delete order: %v", order)
	}
}

func TestPurgeAccountsReportsDeleteFailure(t *testing.T) {
	er := &fakeEraser{}
	res := purgeAccounts(context.Background(), []string{"u1"}, er,
		func(context.Context, string) error { return errors.New("locked") })
	if res.Purged != 0 || res.Failed["u1"] == nil || len(er.calls) != 1 {
		t.Fatalf("result: %+v, erasures %v", res, er.calls)
	}
}
