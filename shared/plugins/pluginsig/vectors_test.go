package pluginsig

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Story 54.14 T01–T05, T07, T08 (go): the verifier on the vectors the TypeScript signer
// made (store/scripts/gen-vectors.ts); Rust and TypeScript check the same file.

type expect struct {
	OK             bool   `json:"ok"`
	Level          string `json:"level"`
	Code           string `json:"code"`
	Step           string `json:"step"`
	Version        int64  `json:"version"`
	Matches        bool   `json:"matches"`
	DeveloperKeyID string `json:"developer_key_id"`
}

type vectorFile struct {
	Now       string            `json:"now"`
	Roots     map[string]string `json:"roots"`
	StoreKeys map[string]string `json:"store_keys"`
	Packages  []struct {
		Name    string `json:"name"`
		Package struct {
			Files        map[string]string `json:"files"`
			FilesJSON    string            `json:"files_json"`
			DeveloperSig *string           `json:"developer_sig"`
			StoreSig     *string           `json:"store_sig"`
			PluginID     string            `json:"plugin_id"`
			Version      string            `json:"version"`
			Caps         string            `json:"capabilities_sha256"`
			StoreKeys    *string           `json:"store_keys"`
		} `json:"package"`
		Expect expect `json:"expect"`
	} `json:"package_cases"`
	StoreKeysCases []listCase         `json:"store_keys_cases"`
	Revoked        map[string]string  `json:"revoked"`
	RevokedCases   []listCase         `json:"revoked_cases"`
	StoredCases    []storedCase       `json:"revoked_stored_cases"`
	Heads          map[string]string  `json:"heads"`
	HeadCases      []headCase         `json:"head_cases"`
	Catalogs       map[string]string  `json:"catalogs"`
	CatalogCases   []listCase         `json:"catalog_cases"`
	Entitlements   []entitlementCase  `json:"entitlement_cases"`
	RootCases      []rootCase         `json:"root_cases"`
	Keys           map[string]RootKey `json:"keys"`
}

type listCase struct {
	Name     string `json:"name"`
	Raw      string `json:"raw"`
	APIMajor int64  `json:"api_major"`
	State    State  `json:"state"`
	Now      string `json:"now"`
	Expect   expect `json:"expect"`
}

type storedCase struct {
	Name          string  `json:"name"`
	Stored        *string `json:"stored"`
	Embedded      *string `json:"embedded"`
	ExpectVersion int64   `json:"expect_version"`
	ExpectExpired bool    `json:"expect_expired"`
}

type headCase struct {
	Name   string `json:"name"`
	Raw    string `json:"raw"`
	List   string `json:"list"`
	Expect expect `json:"expect"`
}

type entitlementCase struct {
	Name   string `json:"name"`
	Raw    string `json:"raw"`
	Expect expect `json:"expect"`
}

type rootCase struct {
	Name   string `json:"name"`
	Old    string `json:"old"`
	Next   string `json:"next"`
	Expect expect `json:"expect"`
}

func loadVectors(t *testing.T) *vectorFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", "signatures", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v vectorFile
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tt, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return tt
}

func (v *vectorFile) keys(t *testing.T, name string) *StoreKeys {
	t.Helper()
	root, err := ParseRoot([]byte(v.Roots["v1"]))
	if err != nil {
		t.Fatal(err)
	}
	k, err := root.VerifyStoreKeys([]byte(v.StoreKeys[name]), State{}, mustTime(t, v.Now))
	if err != nil {
		t.Fatalf("store keys %s: %v", name, err)
	}
	return k
}

func checkOutcome(t *testing.T, err error, e expect) {
	t.Helper()
	if e.OK {
		if err != nil {
			t.Fatalf("want ok, got %v", err)
		}
		return
	}
	code, step := CodeOf(err)
	if code != e.Code || step != e.Step {
		t.Fatalf("want %s at %s, got %v", e.Code, e.Step, err)
	}
}

func TestVectorsCanonical(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "testdata", "signatures", "canonical.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []struct {
			Name    string `json:"name"`
			Input   string `json:"input"`
			Output  string `json:"output"`
			Refused bool   `json:"refused"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			out, err := Canonical([]byte(c.Input))
			if c.Refused {
				if err == nil {
					t.Fatalf("want refused, got %q", out)
				}
				return
			}
			if err != nil || string(out) != c.Output {
				t.Fatalf("got %q, %v; want %q", out, err, c.Output)
			}
		})
	}
}

func TestVectorsPackages(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.Packages {
		t.Run(c.Name, func(t *testing.T) {
			p := c.Package
			var keys *StoreKeys
			if p.StoreKeys != nil {
				keys = v.keys(t, *p.StoreKeys)
			}
			in := Package{FilesJSON: []byte(p.FilesJSON), PluginID: p.PluginID, Version: p.Version, CapabilitiesSHA256: p.Caps,
				Open: func(path string) (io.ReadCloser, error) {
					b, err := base64.StdEncoding.DecodeString(p.Files[path])
					if err != nil || p.Files[path] == "" {
						return nil, os.ErrNotExist
					}
					return io.NopCloser(bytes.NewReader(b)), nil
				}}
			if p.DeveloperSig != nil {
				in.DeveloperSig = []byte(*p.DeveloperSig)
			}
			if p.StoreSig != nil {
				in.StoreSig = []byte(*p.StoreSig)
			}
			res, err := VerifyPackage(in, keys)
			checkOutcome(t, err, c.Expect)
			if c.Expect.OK && res.Level != c.Expect.Level {
				t.Fatalf("level %s, want %s", res.Level, c.Expect.Level)
			}
			if c.Expect.DeveloperKeyID != "" && res.DeveloperKeyID != c.Expect.DeveloperKeyID {
				t.Fatalf("developer key %s", res.DeveloperKeyID)
			}
		})
	}
}

func TestVectorsStoreKeys(t *testing.T) {
	v := loadVectors(t)
	root, err := ParseRoot([]byte(v.Roots["v1"]))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range v.StoreKeysCases {
		t.Run(c.Name, func(t *testing.T) {
			k, err := root.VerifyStoreKeys([]byte(v.StoreKeys[c.Raw]), c.State, mustTime(t, c.Now))
			checkOutcome(t, err, c.Expect)
			if c.Expect.OK && k.Version != c.Expect.Version {
				t.Fatalf("version %d", k.Version)
			}
		})
	}
	empty, err := ParseRoot([]byte(v.Roots["empty"]))
	if err != nil {
		t.Fatal(err)
	}
	_, err = empty.VerifyStoreKeys([]byte(v.StoreKeys["v3"]), State{}, mustTime(t, v.Now))
	checkOutcome(t, err, expect{Code: CodeSignatureInvalid, Step: StepRoot})
}

func TestVectorsRevoked(t *testing.T) {
	v := loadVectors(t)
	keys := v.keys(t, "v3")
	for _, c := range v.RevokedCases {
		t.Run(c.Name, func(t *testing.T) {
			r, err := keys.VerifyRevoked([]byte(v.Revoked[c.Raw]), c.State, mustTime(t, c.Now))
			checkOutcome(t, err, c.Expect)
			if c.Expect.OK && r.Version != c.Expect.Version {
				t.Fatalf("version %d", r.Version)
			}
		})
	}
	for _, c := range v.StoredCases {
		t.Run("stored: "+c.Name, func(t *testing.T) {
			var stored, embedded *Revoked
			var err error
			if c.Stored != nil {
				if stored, err = keys.LoadStoredRevoked([]byte(v.Revoked[*c.Stored])); err != nil {
					t.Fatal(err)
				}
			}
			if c.Embedded != nil {
				if embedded, err = keys.LoadStoredRevoked([]byte(v.Revoked[*c.Embedded])); err != nil {
					t.Fatal(err)
				}
			}
			got := NewestRevoked(stored, embedded)
			if got.Version != c.ExpectVersion || got.Expired(State{}, mustTime(t, v.Now)) != c.ExpectExpired {
				t.Fatalf("version %d expired %v", got.Version, got.Expired(State{}, mustTime(t, v.Now)))
			}
		})
	}
	for _, c := range v.HeadCases {
		t.Run("head: "+c.Name, func(t *testing.T) {
			h, err := keys.VerifyRevokedHead([]byte(v.Heads[c.Raw]))
			checkOutcome(t, err, c.Expect)
			if !c.Expect.OK {
				return
			}
			list, err := keys.VerifyRevoked([]byte(v.Revoked[c.List]), State{}, mustTime(t, v.Now))
			if err != nil {
				t.Fatal(err)
			}
			if h.Matches(list) != c.Expect.Matches {
				t.Fatalf("matches %v", h.Matches(list))
			}
		})
	}
}

func TestVectorsCatalog(t *testing.T) {
	v := loadVectors(t)
	keys := v.keys(t, "v3")
	for _, c := range v.CatalogCases {
		t.Run(c.Name, func(t *testing.T) {
			cat, err := keys.VerifyCatalog([]byte(v.Catalogs[c.Raw]), c.APIMajor, c.State, mustTime(t, c.Now))
			checkOutcome(t, err, c.Expect)
			if c.Expect.OK && cat.Version != c.Expect.Version {
				t.Fatalf("version %d", cat.Version)
			}
		})
	}
}

func TestVectorsEntitlementKeyUse(t *testing.T) {
	v := loadVectors(t)
	keys := v.keys(t, "v3")
	for _, c := range v.Entitlements {
		t.Run(c.Name, func(t *testing.T) {
			_, err := VerifyEntitlement([]byte(c.Raw), keys, State{}, mustTime(t, v.Now))
			checkOutcome(t, err, c.Expect)
		})
	}
}

func TestVectorsRootChain(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.RootCases {
		t.Run(c.Name, func(t *testing.T) {
			old, err := ParseRoot([]byte(v.Roots[c.Old]))
			if err != nil {
				t.Fatal(err)
			}
			r, err := VerifyRootUpdate(old, []byte(v.Roots[c.Next]))
			checkOutcome(t, err, c.Expect)
			if c.Expect.OK && r.Version != c.Expect.Version {
				t.Fatalf("version %d", r.Version)
			}
		})
	}
}

// Go signs what TypeScript signs: the same seed and object give the same envelope.
func TestGoSignerMatchesTheVectors(t *testing.T) {
	v := loadVectors(t)
	seed := sha256Sum([]byte("maktaba-plugins TEST KEY, never for production: store-1"))
	s := NewSigner(seed[:])
	if s.ID != v.Keys["store1"].ID || s.Public() != v.Keys["store1"].Public {
		t.Fatalf("key %s / %s", s.ID, s.Public())
	}
	var env Envelope
	if err := json.Unmarshal([]byte(v.Revoked["v120"]), &env); err != nil {
		t.Fatal(err)
	}
	var signed any
	_ = json.Unmarshal(env.Signed, &signed)
	out, err := SignEnvelope(KindRevoked, json.RawMessage(env.Signed), s)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != v.Revoked["v120"] {
		t.Fatalf("Go's envelope differs:\n%s\n%s", out, v.Revoked["v120"])
	}
}

func sha256Sum(b []byte) [32]byte { return sha256.Sum256(b) }
