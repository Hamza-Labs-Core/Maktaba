package cloudlink

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The agent passes a TV code's lookup and claim to the cloud as the home (its server secret),
// refuses any other path or method, and says 502 when the cloud is out of reach.
func TestTheAgentPassesTVCodesToTheCloudAsTheHome(t *testing.T) {
	var seen []string
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, _ := r.BasicAuth()
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+id+":"+secret+" "+string(body))
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"error":"already_claimed"}`)
			return
		}
		_, _ = io.WriteString(w, `{"platform":"tizen"}`)
	}))
	h := TVCodesHandler(&LinkReporter{CloudAPI: cloud.URL, Creds: Credentials{ServerID: "srv-1", Secret: "s3"}})
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	if rec := do("GET", "/admin/cloud-link/tv-codes/ABCD-2345", ""); rec.Code != 200 || rec.Body.String() != `{"platform":"tizen"}` {
		t.Fatalf("lookup: %d %s", rec.Code, rec.Body)
	}
	if rec := do("POST", "/admin/cloud-link/tv-codes/ABCD-2345/claim", `{"grant":"g"}`); rec.Code != 409 {
		t.Fatalf("claim: %d %s", rec.Code, rec.Body)
	}
	want := []string{
		"GET /v1/homes/srv-1/tv-codes/ABCD-2345 srv-1:s3 ",
		`POST /v1/homes/srv-1/tv-codes/ABCD-2345/claim srv-1:s3 {"grant":"g"}`,
	}
	if strings.Join(seen, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the cloud saw:\n%s", strings.Join(seen, "\n"))
	}
	for _, c := range [][2]string{{"POST", "/admin/cloud-link/tv-codes/ABCD-2345"}, {"GET", "/admin/cloud-link/tv-codes/ABCD-2345/claim"},
		{"GET", "/admin/cloud-link/tv-codes/../../servers"}, {"GET", "/admin/cloud-link/tv-codes/"}} {
		if rec := do(c[0], c[1], ""); rec.Code != 404 {
			t.Fatalf("%s %s: %d", c[0], c[1], rec.Code)
		}
	}
	cloud.Close()
	if rec := do("GET", "/admin/cloud-link/tv-codes/ABCD-2345", ""); rec.Code != http.StatusBadGateway {
		t.Fatalf("cloud down: %d", rec.Code)
	}
}
