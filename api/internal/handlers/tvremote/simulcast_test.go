package tvremote

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Story 32.13 over HTTP: admin only, the key answered masked, the rights
// confirmation (409 rights_confirmation_required with its code), the delete
// guard, start/stop.
func TestSimulcastRoutes(t *testing.T) {
	h, _, _, _ := remoteServer(t)
	admin := &principal.Principal{UserID: "admin-1", IsAdmin: true}
	viewer := &principal.Principal{UserID: "u1"}
	for _, rt := range [][2]string{{"GET", "/api/tv/simulcast"}, {"POST", "/api/tv/simulcast/destinations"},
		{"PUT", "/api/tv/simulcast/destinations/x"}, {"DELETE", "/api/tv/simulcast/destinations/x"},
		{"POST", "/api/tv/simulcast/destinations/x/test"}, {"GET", "/api/channels/" + chID + "/simulcast"},
		{"PUT", "/api/channels/" + chID + "/simulcast"}, {"POST", "/api/tv/simulcast/outputs/x/start"},
		{"POST", "/api/tv/simulcast/outputs/x/stop"}} {
		if rec := as(h, rt[0], rt[1], `{}`, viewer); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a viewer: %d", rt[0], rt[1], rec.Code)
		}
	}
	const key = "abcd-1234-efgh-5678"
	rec := as(h, "POST", "/api/tv/simulcast/destinations", `{"name":"YT","kind":"youtube","stream_key":"`+key+`"}`, admin)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), key) || !strings.Contains(rec.Body.String(), "••••5678") {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var d struct {
		ID        string `json:"id"`
		IngestURL string `json:"ingest_url"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &d)
	if d.IngestURL != "rtmp://a.rtmp.youtube.com/live2" {
		t.Errorf("ingest %s", d.IngestURL)
	}
	if rec := as(h, "POST", "/api/tv/simulcast/destinations", `{"name":"X","kind":"rtmp","ingest_url":"http://x/y","stream_key":"k"}`, admin); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), `"code":"invalid_destination"`) {
		t.Errorf("bad url: %d %s", rec.Code, rec.Body)
	}
	body := `[{"destination_id":"` + d.ID + `","mode":"schedule","rules":[{"kind":"live"}],"enabled":true}]`
	rec = as(h, "PUT", "/api/channels/"+chID+"/simulcast", body, admin)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"rights_confirmation_required"`) {
		t.Fatalf("without rights: %d %s", rec.Code, rec.Body)
	}
	body = `[{"destination_id":"` + d.ID + `","mode":"schedule","rules":[{"kind":"live"}],"enabled":true,"rights_confirmed":true}]`
	rec = as(h, "PUT", "/api/channels/"+chID+"/simulcast", body, admin)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"rights_confirmed_by":"admin-1"`) {
		t.Fatalf("with rights: %d %s", rec.Code, rec.Body)
	}
	var v struct {
		Attachments []struct {
			ID string `json:"id"`
		} `json:"attachments"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if len(v.Attachments) != 1 {
		t.Fatalf("attachments %s", rec.Body)
	}
	if rec := as(h, "PUT", "/api/channels/"+chID+"/simulcast", `[{"destination_id":"`+d.ID+`","mode":"schedule","rules":[],"enabled":false}]`, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("a schedule without rules: %d", rec.Code)
	}
	if rec := as(h, "POST", "/api/tv/simulcast/outputs/"+v.Attachments[0].ID+"/start", ``, admin); rec.Code != http.StatusAccepted {
		t.Errorf("start: %d %s", rec.Code, rec.Body)
	}
	if rec := as(h, "POST", "/api/tv/simulcast/outputs/"+v.Attachments[0].ID+"/stop", ``, admin); rec.Code != http.StatusAccepted {
		t.Errorf("stop: %d %s", rec.Code, rec.Body)
	}
	if rec := as(h, "POST", "/api/tv/simulcast/destinations/"+d.ID+"/test", ``, admin); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Errorf("test: %d %s", rec.Code, rec.Body)
	}
	if rec := as(h, "GET", "/api/tv/simulcast", ``, admin); rec.Code != 200 || strings.Contains(rec.Body.String(), key) ||
		!strings.Contains(rec.Body.String(), `"cap_hours":60`) {
		t.Errorf("status: %d %s", rec.Code, rec.Body)
	}
	if rec := as(h, "DELETE", "/api/tv/simulcast/destinations/"+d.ID, ``, admin); rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := as(h, "DELETE", "/api/tv/simulcast/destinations/"+d.ID, ``, admin); rec.Code != http.StatusNotFound {
		t.Errorf("delete twice: %d", rec.Code)
	}
}
