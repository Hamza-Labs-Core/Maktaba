package cluster

import (
	"context"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
)

// Epic 42 story 42.2: the API's own announcement (a home without the supervisor) says
// the home's full instance id, its name, and path only when the web is not at the root.
func TestAdvertiserTXTForTheHomePicker(t *testing.T) {
	pub := &discovery.NoopPublisher{}
	a := &Advertiser{Publisher: pub, Port: 8080, Version: "1.6.0", SchemaRev: 161}
	id := Identity{InstanceID: "0A1B2C3D-0000-4000-8000-000000000000", Name: "Kitchen"}
	if err := a.Announce(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	txt := pub.Last().TXT
	if txt["id"] != "0a1b2c3d-0000-4000-8000-000000000000" || txt["home"] != txt["id"] || txt["tls"] != "0" ||
		txt["name"] != "Kitchen" || txt["role"] != "home" ||
		txt["version"] != "1.6.0" || txt["schema_rev"] != "161" {
		t.Fatalf("txt = %v", txt)
	}
	if _, ok := txt["path"]; ok {
		t.Fatalf("root path announced: %v", txt)
	}
	a.Path = "/maktaba/"
	if p := a.Service(id).TXT["path"]; p != "/maktaba" {
		t.Fatalf("path = %q", p)
	}
	a.Path = "/"
	if _, ok := a.Service(id).TXT["path"]; ok {
		t.Fatal(`"/" announced`)
	}
	a.TLS = true
	if v := a.Service(id).TXT["tls"]; v != "1" {
		t.Fatalf("tls = %q", v)
	}
}
