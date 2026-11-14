package authz

import (
	"context"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// AllLibraries names every library, one by one, whoever reads them (a cloud code's admin TV,
// Epic 58 story 58.03); LibrariesFor stays the person's grants.
func TestAllLibrariesNamesEveryLibrary(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	const a, b = "00000000-0000-4000-8000-0000000ac10a", "00000000-0000-4000-8000-0000000ac10b"
	if _, err := db.Exec(`INSERT INTO libraries (id, name, roots) VALUES ($1,'A','{/m/a}'), ($2,'B','{/m/b}')`, b, a); err != nil {
		t.Fatal(err)
	}
	s := &ACLStore{DB: db}
	got, err := s.AllLibraries(context.Background())
	if err != nil || strings.Join(got, ",") != a+","+b {
		t.Fatalf("all = %v %v", got, err)
	}
	if mine, err := s.LibrariesFor(context.Background(), "00000000-0000-4000-8000-000000000001"); err != nil || len(mine) != 0 {
		t.Fatalf("no grants = %v %v", mine, err)
	}
}
