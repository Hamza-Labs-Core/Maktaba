package cluster

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

func strp(s string) *string { return &s }

// Story 59.13: the NAS home's shares are an admin's. No one signed in, a viewer (even
// one with every library), a cloud play grant and a manage grant without admin are
// refused before any read; an admin is answered.
func TestHomeSharesRoles(t *testing.T) {
	f := newFixture(t)
	f.h.Storage = &cluster.MemoryStorage{Storage: json.RawMessage(`{"home_server":"diskstation","home_platform":"dsm"}`)}
	others := map[string]*principal.Principal{
		"no one":         nil,
		"viewer":         viewer(),
		"all libraries":  {UserID: "u3", AccessAllLibraries: true},
		"cloud play":     {UserID: "u4", CloudScope: principal.CloudScopePlay},
		"cloud manage":   {UserID: "u5", CloudScope: principal.CloudScopeManage},
		"password stale": {UserID: "u6", MustChangePassword: true},
	}
	for _, c := range []struct{ method, body string }{
		{"GET", ""},
		{"PUT", `{"addresses":[],"shares":[{"path":"/volume1/Movies","share":"Movies"}]}`},
	} {
		for who, p := range others {
			if rec := f.do(c.method, "/api/cluster/storage/home-shares", c.body, p); rec.Code != http.StatusForbidden {
				t.Errorf("%s as %s: %d, want 403", c.method, who, rec.Code)
			}
		}
		if rec := f.do(c.method, "/api/cluster/storage/home-shares", c.body, admin()); rec.Code != http.StatusOK {
			t.Errorf("%s as admin: %d %s", c.method, rec.Code, rec.Body.String())
		}
	}
}

func TestHomeSharesShowsTheNASAndItsFolders(t *testing.T) {
	f := newFixture(t)
	mem := &cluster.MemoryStorage{
		Storage: json.RawMessage(`{"home_server":"diskstation","home_platform":"dsm","unnamed_roots":["/mnt/usb/clips"]}`),
		Access: []cluster.RootAccess{
			{Path: "smb://diskstation/Movies", Access: strp("ro")},
			{Path: "/volume1/Recordings", Access: strp("rw")},
		},
	}
	f.h.Storage = mem
	if _, err := mem.PutServer(t.Context(), cluster.StorageServer{Name: "diskstation",
		Addresses: []string{"diskstation.local", "192.168.1.20"},
		Exports:   map[string]string{"/volume1/Movies": "Movies", "/volume1/Recordings": "Recordings", "/media/TV": "TV", "/volume1/Private": ""}}); err != nil {
		t.Fatal(err)
	}
	var hs HomeShares
	decode(t, f.do("GET", "/api/cluster/storage/home-shares", "", admin()), http.StatusOK, &hs)
	if !hs.NAS || hs.Server != "diskstation" || hs.Platform != "dsm" || len(hs.Addresses) != 2 {
		t.Fatalf("home %+v", hs)
	}
	if len(hs.Shares) != 4 || hs.Shares[0].Path != "/media/TV" || hs.Shares[0].HostPath || !hs.OtherPath {
		t.Fatalf("shares %+v", hs.Shares)
	}
	var movies, private HomeShare
	for _, s := range hs.Shares {
		switch s.Path {
		case "/volume1/Movies":
			movies = s
		case "/volume1/Private":
			private = s
		}
	}
	if movies.Rule != "Movies" || !movies.HostPath || movies.LocalOnly || !private.LocalOnly {
		t.Fatalf("movies %+v private %+v", movies, private)
	}
	if len(hs.Unnamed) != 1 || !hs.WritableShare || len(hs.Notices) != 0 {
		t.Fatalf("unnamed %v writable %v notices %v", hs.Unnamed, hs.WritableShare, hs.Notices)
	}

	// every shared folder read-only: the notice, and a join's answer carries it
	mem.Access = []cluster.RootAccess{{Path: "smb://diskstation/Movies", Access: strp("ro")},
		{Path: "/volume1/Private/x", Access: strp("rw")}}
	decode(t, f.do("GET", "/api/cluster/storage/home-shares", "", admin()), http.StatusOK, &hs)
	if hs.WritableShare || len(hs.Notices) != 1 || hs.Notices[0] != NoWritableShare {
		t.Fatalf("read-only NAS: %+v", hs)
	}
	if n := f.h.joinNotices(t.Context()); len(n) != 1 || n[0].Code != NoWritableShare || n[0].Message == "" {
		t.Fatalf("join notices %+v", n)
	}
}

func TestPutHomeSharesNamesTheFolders(t *testing.T) {
	f := newFixture(t)
	mem := &cluster.MemoryStorage{Storage: json.RawMessage(`{"home_server":"truenas","home_platform":"truenas"}`)}
	f.h.Storage = mem
	if _, err := mem.PutServer(t.Context(), cluster.StorageServer{Name: "truenas", CharMap: "fullwidth"}); err != nil {
		t.Fatal(err)
	}
	var hs HomeShares
	decode(t, f.do("PUT", "/api/cluster/storage/home-shares",
		`{"server":"ignored","addresses":["truenas.local","10.0.0.5"],"shares":[{"path":"/mnt/tank/media/","share":"media"},{"path":"/mnt/tank/home","share":""}]}`,
		admin()), http.StatusOK, &hs)
	if hs.Server != "truenas" || len(hs.Shares) != 2 || hs.Shares[0].Path != "/mnt/tank/home" || !hs.Shares[0].LocalOnly ||
		hs.Shares[1].Path != "/mnt/tank/media" || hs.Shares[1].Share != "media" {
		t.Fatalf("saved %+v", hs)
	}
	servers, _ := mem.Servers(t.Context())
	if len(servers) != 1 || servers[0].CharMap != "fullwidth" {
		t.Fatalf("the record's character map is kept: %+v", servers)
	}
	for _, body := range []string{
		`{"addresses":[],"shares":[{"path":"relative","share":"x"}]}`,
		`{"addresses":[],"shares":[{"path":"/a","share":"x/y"}]}`,
	} {
		if rec := f.do("PUT", "/api/cluster/storage/home-shares", body, admin()); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, rec.Code)
		}
	}
}

func TestHomeSharesOnAHomeThatIsNotANAS(t *testing.T) {
	f := newFixture(t)
	f.h.Storage = &cluster.MemoryStorage{Storage: json.RawMessage(`{"read":"ok"}`)}
	var hs HomeShares
	decode(t, f.do("GET", "/api/cluster/storage/home-shares", "", admin()), http.StatusOK, &hs)
	if hs.NAS || hs.Shares == nil || hs.Notices == nil {
		t.Fatalf("not a NAS: %+v", hs)
	}
	if rec := f.do("PUT", "/api/cluster/storage/home-shares", `{"addresses":[],"shares":[]}`, admin()); rec.Code != http.StatusConflict {
		t.Fatalf("no record and no name: %d", rec.Code)
	}
	if n := f.h.joinNotices(t.Context()); n != nil {
		t.Fatalf("no notices off a NAS: %+v", n)
	}
}
