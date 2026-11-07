package pluginsig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Story 54.14 T06: the store online key rotation drill, the home's half. The `store` CI
// job makes the files with the TypeScript signer (store/scripts/drill-rotation.ts) and runs
// this with MAKTABA_STORE_DRILL naming their folder; without it the test is skipped.
func TestRotationDrill(t *testing.T) {
	dir := os.Getenv("MAKTABA_STORE_DRILL")
	if dir == "" {
		t.Skip("MAKTABA_STORE_DRILL unset: the drill runs in the store CI job")
	}
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	root, err := ParseRoot(read("root.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var st State
	v1, err := root.VerifyStoreKeys(read("store-keys-v1.json"), st, now)
	if err != nil {
		t.Fatal(err)
	}
	st.AcceptStoreKeys(v1)
	v2, err := root.VerifyStoreKeys(read("store-keys-v2.json"), st, now)
	if err != nil {
		t.Fatalf("homes accept the new store-keys.json: %v", err)
	}
	st.AcceptStoreKeys(v2)
	if _, err := root.VerifyStoreKeys(read("store-keys-v1.json"), st, now); err == nil {
		t.Fatal("the old store-keys.json is accepted after the new one")
	}
	var pkgs []struct {
		PluginID     string `json:"plugin_id"`
		Version      string `json:"version"`
		Caps         string `json:"capabilities_sha256"`
		FilesJSON    string `json:"files_json"`
		DeveloperSig string `json:"developer_sig"`
		StoreSigOld  string `json:"store_sig_old"`
		StoreSigNew  string `json:"store_sig_new"`
	}
	if err := json.Unmarshal(read("packages.json"), &pkgs); err != nil || len(pkgs) == 0 {
		t.Fatalf("packages: %v", err)
	}
	for _, p := range pkgs {
		in := Package{FilesJSON: []byte(p.FilesJSON), DeveloperSig: []byte(p.DeveloperSig), PluginID: p.PluginID,
			Version: p.Version, CapabilitiesSHA256: p.Caps}
		in.StoreSig = []byte(p.StoreSigNew)
		if r, err := VerifyPackage(in, v2); err != nil || r.Level != LevelReviewed {
			t.Fatalf("%s re-signed: %v", p.PluginID, err)
		}
		in.StoreSig = []byte(p.StoreSigOld)
		if _, err := VerifyPackage(in, v2); err == nil {
			t.Fatalf("%s still signed by the removed key is accepted", p.PluginID)
		}
	}
	if _, err := v2.VerifyRevoked(read("revoked-by-old-key.json"), st, now); err == nil {
		t.Fatal("a revocation list the removed key signed is accepted")
	}
	if _, err := v2.VerifyRevoked(read("revoked-by-new-key.json"), st, now); err != nil {
		t.Fatalf("the new key's revocation list: %v", err)
	}
}
