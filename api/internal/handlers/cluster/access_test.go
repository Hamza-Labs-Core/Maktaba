package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

// usableRoles are fake Roles that also say whether a role logs in, and the server's port.
type usableRoles struct {
	*fakeRoles
	usable  bool
	port    int
	termErr error
}

func (u *usableRoles) Usable(context.Context, string) (bool, error) { return u.usable, nil }
func (u *usableRoles) ServerPort(context.Context) (int, error)      { return u.port, nil }
func (u *usableRoles) Term(context.Context) (int64, error) {
	if u.termErr != nil {
		return 0, u.termErr
	}
	return 4, nil
}

// The home owns its machines' database access: an approved machine whose role does not log
// in on the home's current database is said to be so, and its access is made again (its
// role, a new password, the database's own port and CA) for the machine to follow it; a
// machine that is not approved is not given one.
func TestMachineAccessRemade(t *testing.T) {
	f := newFixture(t)
	f.openJoins()
	jr := f.join()
	decode(t, f.approve(nodeCode(t, jr.HomePubkey)), http.StatusOK, nil)
	roles := &usableRoles{fakeRoles: f.roles, usable: false, port: 55432}
	f.h.Roles = roles
	ctx := context.Background()
	if ok, err := f.h.AccessUsable(ctx, nodeID); err != nil || ok {
		t.Fatalf("a role the home's database lacks: usable %v (%v)", ok, err)
	}
	joinPW := f.roles.created[cluster.RoleName(nodeID)]
	raw, err := f.h.GrantAccess(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	var a machineAccess
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	if a.Role != cluster.RoleName(nodeID) || a.Password == "" || a.Password == joinPW || f.roles.created[a.Role] != a.Password ||
		a.DBName != "maktaba" || a.DBPort != 55432 || a.DBHost != "" || a.Term != 4 {
		t.Fatalf("access %+v (created %v)", a, f.roles.created)
	}
	// the host from the home's own database url is never handed out (compose's "postgres");
	// a setting names one, and the port the machines reach (a published port)
	t.Setenv("DATABASE_URL", "postgres://maktaba:x@postgres:5432/maktaba")
	raw, _ = f.h.GrantAccess(ctx, nodeID)
	_ = json.Unmarshal(raw, &a)
	if a.DBHost != "" {
		t.Fatalf("the home's own database host handed out: %q", a.DBHost)
	}
	t.Setenv("MAKTABA_CLUSTER_DB_HOST", "db.lan")
	t.Setenv("MAKTABA_CLUSTER_DB_PORT", "15432")
	raw, _ = f.h.GrantAccess(ctx, nodeID)
	_ = json.Unmarshal(raw, &a)
	if host, port, name, err := f.h.AccessTarget(ctx, nodeID); a.DBHost != "db.lan" || a.DBPort != 15432 ||
		host != "db.lan" || port != 15432 || name != "maktaba" || err != nil {
		t.Fatalf("named host and port: %+v; target %s %d %s %v", a, host, port, name, err)
	}
	t.Setenv("MAKTABA_CLUSTER_DB_HOST", "")
	t.Setenv("MAKTABA_CLUSTER_DB_PORT", "")
	// a term the database cannot say: no access, and the password left as it was
	before := f.roles.created[a.Role]
	roles.termErr = errors.New("the database went away")
	if _, err := f.h.GrantAccess(ctx, nodeID); err == nil || f.roles.created[a.Role] != before {
		t.Fatalf("an access with no term: %v (password changed: %v)", err, f.roles.created[a.Role] != before)
	}
	roles.termErr = nil
	// the bundled Postgres: its port, as joins hand it; its CA unreadable: no access
	// without it (the machines would be left a connection they cannot verify)
	f.h.PGBundled = true
	raw, _ = f.h.GrantAccess(ctx, nodeID)
	_ = json.Unmarshal(raw, &a)
	if a.DBPort != 6543 {
		t.Fatalf("bundled port %d", a.DBPort)
	}
	f.h.PGCA = filepath.Join(t.TempDir(), "no-ca.pem")
	if _, err := f.h.GrantAccess(ctx, nodeID); err == nil {
		t.Fatal("an access without the bundled database's CA")
	}
	f.h.PGCA = ""
	roles.usable = true
	if ok, _ := f.h.AccessUsable(ctx, nodeID); !ok {
		t.Fatal("a role that logs in")
	}
	// a revoked machine is not this home's to give access to
	decode(t, f.do("POST", "/api/cluster/nodes/"+nodeID+"/revoke", "", admin()), http.StatusOK, nil)
	if _, err := f.h.GrantAccess(ctx, nodeID); err == nil {
		t.Fatal("a revoked machine was given access")
	}
	if ok, _ := f.h.AccessUsable(ctx, nodeID); !ok {
		t.Fatal("a revoked machine is not asked about")
	}
}

// A node's view (the Machines page) carries its agent's last update.state, its login to
// the home's database among it, while it is connected.
func TestNodeViewCarriesItsLiveState(t *testing.T) {
	f := newFixture(t)
	f.openJoins()
	jr := f.join()
	decode(t, f.approve(nodeCode(t, jr.HomePubkey)), http.StatusOK, nil)
	f.h.LiveStates = func() map[string]json.RawMessage {
		return map[string]json.RawMessage{nodeID: json.RawMessage(`{"version":"1.5.0","home_db":{"ok":false,"code":"auth_failed"}}`)}
	}
	var o struct {
		Nodes []struct {
			ID          string `json:"id"`
			UpdateState struct {
				HomeDB struct {
					OK   bool   `json:"ok"`
					Code string `json:"code"`
				} `json:"home_db"`
			} `json:"update_state"`
		} `json:"nodes"`
	}
	decode(t, f.do("GET", "/api/cluster", "", admin()), http.StatusOK, &o)
	if len(o.Nodes) != 1 || o.Nodes[0].UpdateState.HomeDB.OK || o.Nodes[0].UpdateState.HomeDB.Code != "auth_failed" {
		t.Fatalf("the node's view: %+v", o)
	}
}
