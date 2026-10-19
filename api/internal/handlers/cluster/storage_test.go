package cluster

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

// Story 31.3: storage servers, the home's per-share report and override.
func TestStorageServersCRUD(t *testing.T) {
	f := newFixture(t)
	f.h.Storage = &cluster.MemoryStorage{}

	if rec := f.do("GET", "/api/cluster/storage-servers", "", viewer()); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer: %d", rec.Code)
	}
	var s cluster.StorageServer
	decode(t, f.do("PUT", "/api/cluster/storage-servers/Home-NAS",
		`{"addresses":["192.168.50.94","home-nas.local","192.168.50.94"],"char_map":"fullwidth"}`, admin()),
		http.StatusOK, &s)
	if s.Name != "home-nas" || len(s.Addresses) != 2 || s.CharMap != "fullwidth" {
		t.Fatalf("saved %+v", s)
	}
	// an IP change is one edit of the addresses
	decode(t, f.do("PUT", "/api/cluster/storage-servers/home-nas",
		`{"addresses":["192.168.50.10","home-nas.local"]}`, admin()), http.StatusOK, &s)
	var list struct {
		Servers []cluster.StorageServer `json:"servers"`
	}
	decode(t, f.do("GET", "/api/cluster/storage-servers", "", admin()), http.StatusOK, &list)
	if len(list.Servers) != 1 || list.Servers[0].Addresses[0] != "192.168.50.10" || list.Servers[0].CharMap != "none" {
		t.Fatalf("list %+v", list.Servers)
	}
	if rec := f.do("PUT", "/api/cluster/storage-servers/x", `{"char_map":"oops"}`, admin()); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad char map: %d", rec.Code)
	}
	if rec := f.do("PUT", "/api/cluster/storage-servers/x", `{"exports":{"volume1":"O"}}`, admin()); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad export: %d", rec.Code)
	}
	if rec := f.do("DELETE", "/api/cluster/storage-servers/home-nas", "", admin()); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := f.do("DELETE", "/api/cluster/storage-servers/home-nas", "", admin()); rec.Code != http.StatusNotFound {
		t.Fatalf("delete again: %d", rec.Code)
	}
}

func TestHomeStorageAndOverride(t *testing.T) {
	f := newFixture(t)
	mem := &cluster.MemoryStorage{
		Storage: json.RawMessage(`{"read":"ok","write":"ok","shares":[{"share":"smb://nas/Originals","local":"/Volumes/Originals/","read":"ok","write":"ok"}]}`),
	}
	f.h.Storage = mem
	var ov struct {
		Home struct {
			Storage struct {
				Shares []map[string]any `json:"shares"`
			} `json:"storage"`
			PathMap map[string]string `json:"path_map"`
		} `json:"home"`
	}
	decode(t, f.do("GET", "/api/cluster", "", admin()), http.StatusOK, &ov)
	if len(ov.Home.Storage.Shares) != 1 || ov.Home.Storage.Shares[0]["share"] != "smb://nas/Originals" {
		t.Fatalf("home storage %+v", ov.Home.Storage)
	}
	decode(t, f.do("PUT", "/api/cluster/home/path-map",
		`{"path_map":{"smb://nas/Originals/":"/Volumes/O/"}}`, admin()), http.StatusOK, &ov.Home)
	if string(mem.PathMap) != `{"smb://nas/Originals/":"/Volumes/O/"}` {
		t.Fatalf("stored %s", mem.PathMap)
	}
	if rec := f.do("PUT", "/api/cluster/home/path-map", `{"path_map":{"a":1}}`, admin()); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad map: %d", rec.Code)
	}
	decode(t, f.do("PUT", "/api/cluster/home/path-map", `{"path_map":null}`, admin()), http.StatusOK, &ov.Home)
	if mem.PathMap != nil {
		t.Fatalf("not cleared: %s", mem.PathMap)
	}
}

func TestStorageRoutesWithoutAStore(t *testing.T) {
	f := newFixture(t)
	if rec := f.do("GET", "/api/cluster/storage-servers", "", admin()); rec.Code != http.StatusNotFound {
		t.Fatalf("no store: %d", rec.Code)
	}
}

// The automatic path conversion (story 31.3): the home's report on the
// Machines page and the admin's requests.
func TestCanonicalizeStatusAndRequests(t *testing.T) {
	f := newFixture(t)
	mem := &cluster.MemoryStorage{}
	f.h.Storage = mem
	if rec := f.do("GET", "/api/cluster/storage/canonicalize", "", viewer()); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer: %d", rec.Code)
	}
	var st struct {
		State   map[string]any `json:"state"`
		Request *string        `json:"request"`
	}
	decode(t, f.do("GET", "/api/cluster/storage/canonicalize", "", admin()), http.StatusOK, &st)
	if len(st.State) != 0 || st.Request != nil {
		t.Fatalf("before the first look: %+v", st)
	}
	if rec := f.do("POST", "/api/cluster/storage/canonicalize", `{"action":"drop"}`, admin()); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad action: %d", rec.Code)
	}
	if rec := f.do("POST", "/api/cluster/storage/canonicalize", `{"action":"revert"}`, viewer()); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer revert: %d", rec.Code)
	}
	decode(t, f.do("POST", "/api/cluster/storage/canonicalize", `{"action":"revert"}`, admin()), http.StatusAccepted, &st)
	if st.Request == nil || *st.Request != "revert" || *mem.Canon.RequestedBy != "user:u1" {
		t.Fatalf("request %+v", st)
	}
	mem.Canon.State = json.RawMessage(`{"phase":"held","held":true}`)
	var ov struct {
		Home struct {
			Canonicalize struct {
				State   map[string]any `json:"state"`
				Request *string        `json:"request"`
			} `json:"canonicalize"`
		} `json:"home"`
	}
	decode(t, f.do("GET", "/api/cluster", "", admin()), http.StatusOK, &ov)
	if ov.Home.Canonicalize.State["phase"] != "held" || *ov.Home.Canonicalize.Request != "revert" {
		t.Fatalf("overview %+v", ov.Home.Canonicalize)
	}
}
