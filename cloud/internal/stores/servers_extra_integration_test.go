//go:build integration

package stores

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
)

// Story 50.19 §6: a home's extra streams come with every lookup of it (the relay's cache reads
// BySlug), 0 by default.
func TestServersCarryTheirExtraStreams(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	owner, err := NewUsers(pool.DB).Create(ctx, "o-"+uuid.NewString()+"@example.com", "x", "O", false)
	if err != nil {
		t.Fatal(err)
	}
	slug := "s" + uuid.NewString()[:8]
	var id string
	if err := pool.DB.QueryRowContext(ctx, `INSERT INTO servers (owner_user_id, name, slug, server_secret_hash) VALUES ($1, 'h', $2, 'x') RETURNING id`,
		owner.ID, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	s := NewServers(pool.DB)
	if sv, err := s.BySlug(ctx, slug); err != nil || sv.ExtraStreams != 0 {
		t.Fatalf("new: %+v %v", sv, err)
	}
	if _, err := pool.DB.ExecContext(ctx, `UPDATE servers SET extra_streams = 3 WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	for name, get := range map[string]func() (Server, error){
		"BySlug": func() (Server, error) { return s.BySlug(ctx, slug) },
		"ByID":   func() (Server, error) { return s.ByID(ctx, id) },
	} {
		if sv, err := get(); err != nil || sv.ExtraStreams != 3 {
			t.Fatalf("%s: %+v %v", name, sv, err)
		}
	}
	list, err := s.ListByOwner(ctx, owner.ID)
	if err != nil || len(list) != 1 || list[0].ExtraStreams != 3 {
		t.Fatalf("ListByOwner: %+v %v", list, err)
	}
}
