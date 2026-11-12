package components

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// withParental adds the block as the object's last member, whatever the object holds,
// and leaves a body that is no object alone.
func TestWithParental(t *testing.T) {
	block := []byte(`{"restricted":true}`)
	for body, want := range map[string]string{
		`{"a":1}`:      `{"a":1,"parental":{"restricted":true}}`,
		"{\"a\":1}\n ": `{"a":1,"parental":{"restricted":true}}`,
		`{}`:           `{"parental":{"restricted":true}}`,
		`{ }`:          `{ "parental":{"restricted":true}}`,
		`[1]`:          `[1]`,
		``:             ``,
		`}`:            `}`,
	} {
		got := string(withParental([]byte(body), block))
		if got != want {
			t.Errorf("withParental(%q) = %q, want %q", body, got, want)
		}
		if body != "" && body[0] == '{' && !json.Valid([]byte(got)) {
			t.Errorf("withParental(%q) = %q is not JSON", body, got)
		}
	}
	// the shared snapshot is not written to
	shared := []byte(`{"a":1}`)
	_ = withParental(shared[:len(shared):len(shared)], block)
	if string(shared) != `{"a":1}` {
		t.Fatalf("the snapshot changed: %s", shared)
	}
}

// A block that cannot be read is a 503 with Retry-After and the registered problem
// type of its code, never an answer without the block.
func TestCapabilitiesParentalUnreadable(t *testing.T) {
	r := chi.NewRouter()
	(&Capabilities{
		Caps: svc.NewCapabilities(svc.CapabilitiesOptions{}),
		Parental: func(*http.Request, *principal.Principal) (any, error) {
			return nil, errors.New("database is down")
		},
	}).Mount(r)
	rec := getCaps(r, &principal.Principal{UserID: "u1"}, "")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("status %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["code"] != "parental_unavailable" || out["type"] != httperror.Type("parental-unavailable") {
		t.Fatalf("problem %v", out)
	}
	if typ, _ := out["type"].(string); !httperror.Registered(typ) {
		t.Fatalf("type %q is not in the registry", typ)
	}
}
