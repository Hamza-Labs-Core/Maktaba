package cloudlink

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The agent passes a home's report, the replies read and the delete to the cloud as the
// home (its server secret), and the cloud's answer back as it is.
func TestTheAgentPassesReportsToTheCloudAsTheHome(t *testing.T) {
	var seen []string
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, _ := r.BasicAuth()
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" "+id+":"+secret+" "+r.Header.Get("Content-Type")+" "+string(body))
		switch r.Method {
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":41}`)
		case http.MethodGet:
			_, _ = io.WriteString(w, `{"replies":[]}`)
		default:
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer cloud.Close()
	h := ReportsHandler(&LinkReporter{CloudAPI: cloud.URL, Creds: Credentials{ServerID: "srv-1", Secret: "s3"}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/cloud-link/reports", strings.NewReader("--b\r\n"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=b")
	h(rec, req)
	if rec.Code != http.StatusCreated || rec.Body.String() != `{"id":41}` {
		t.Fatalf("post: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/admin/cloud-link/reports/replies?since=2026-11-07T00%3A00%3A00Z", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("replies: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodDelete, "/admin/cloud-link/reports", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("delete: %d %v", rec.Code, rec.Header())
	}
	want := []string{
		"POST /v1/homes/srv-1/reports srv-1:s3 multipart/form-data; boundary=b --b\r\n",
		"GET /v1/homes/srv-1/reports/replies?since=2026-11-07T00%3A00%3A00Z srv-1:s3  ",
		"DELETE /v1/homes/srv-1/reports srv-1:s3  ",
	}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Fatalf("the cloud saw:\n%q\nwant:\n%q", seen, want)
	}
	// anything else is not passed on
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/admin/cloud-link/reports", nil))
	if rec.Code != http.StatusNotFound || len(seen) != 3 {
		t.Fatalf("a GET of the reports: %d, the cloud saw %d", rec.Code, len(seen))
	}
}

// An unreachable cloud is a 502: the home keeps the report.
func TestAnUnreachableCloudIsABadGateway(t *testing.T) {
	h := ReportsHandler(&LinkReporter{CloudAPI: "http://127.0.0.1:1", Creds: Credentials{ServerID: "srv-1", Secret: "s3"}})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/admin/cloud-link/reports", strings.NewReader("x")))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("%d", rec.Code)
	}
}
