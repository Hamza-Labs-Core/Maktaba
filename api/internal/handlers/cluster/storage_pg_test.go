package cluster

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// TestPGStorageServers runs the story 31.3 store on a real, migrated
// Postgres (MAKTABA_TEST_PG_URL): servers by name, the home's report.
func TestPGStorageServers(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	st := &cluster.PGStorage{DB: db}
	s, err := st.PutServer(ctx, cluster.StorageServer{Name: "Home-NAS", Addresses: []string{"192.168.50.94"}})
	if err != nil || s.Name != "home-nas" || s.ID == "" {
		t.Fatalf("put: %+v %v", s, err)
	}
	if _, err := st.PutServer(ctx, cluster.StorageServer{Name: "home-nas", Addresses: []string{"192.168.50.10"}, CharMap: "fullwidth"}); err != nil {
		t.Fatal(err)
	}
	list, err := st.Servers(ctx)
	if err != nil || len(list) != 1 || list[0].Addresses[0] != "192.168.50.10" || list[0].CharMap != "fullwidth" || list[0].ID != s.ID {
		t.Fatalf("list %+v %v", list, err)
	}
	h := &Handler{Store: &cluster.PGStore{DB: db}, Roles: &cluster.PGRoles{DB: db}}
	if _, err := h.Bootstrap(ctx, "home-test"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetHomePathMap(ctx, json.RawMessage(`{"smb://home-nas/Originals/": "/Volumes/O/"}`)); err != nil {
		t.Fatal(err)
	}
	_, pm, err := st.HomeStorage(ctx)
	if err != nil || len(pm) == 0 {
		t.Fatalf("home map %s %v", pm, err)
	}
	if err := st.SetHomePathMap(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, pm, _ = st.HomeStorage(ctx); pm != nil {
		t.Fatalf("not cleared: %s", pm)
	}
	if err := st.DeleteServer(ctx, "HOME-NAS"); err != nil {
		t.Fatal(err)
	}
}
