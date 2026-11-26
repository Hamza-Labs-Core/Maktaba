package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/cloudlink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Issue #1172: a refused cloud link assertion (422) uses cloud-link-refused,
// not invalid-credentials (which is 401).
func TestCompleteCloudLinkRefusedProblemType(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "identity.json")
	raw, err := json.Marshal(map[string]string{
		"server_id":     "srv-1",
		"cloud_api":     "https://cloud.example",
		"assertion_key": base64.StdEncoding.EncodeToString(pub),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	h := &Handler{
		Cloud: &CloudLinking{
			Store:    &cloudlink.Store{},
			Identity: &cloudlink.IdentitySource{Path: path},
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/me/cloud-link", strings.NewReader(`{"assertion":"not-a-valid-assertion"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "user-1"}))
	rec := httptest.NewRecorder()
	h.CompleteCloudLink(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body = %s, want 422", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["type"] != TypeCloudLinkRefused {
		t.Fatalf("type = %v, want %s", body["type"], TypeCloudLinkRefused)
	}
}
