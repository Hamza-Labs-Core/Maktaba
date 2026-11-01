package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	subh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/subscriptions"
)

// identityStore is a cluster.Store that answers Identity only.
type identityStore struct {
	cluster.Store
	id  cluster.Identity
	err error
}

func (s identityStore) Identity(context.Context) (cluster.Identity, error) { return s.id, s.err }

// Epic 42 story 42.2: the home's URLs are its web address, the same on its .local name,
// then the relay address of a claimed home; its id and name are the instance identity's.
func TestHomeDirectory(t *testing.T) {
	d := &homeDirectory{
		WebURL:    "http://192.168.1.5:8088/",
		LocalHost: "Studio.local",
		HostName:  "studio",
		Cloud:     func() subh.CloudLink { return subh.CloudLink{Claimed: true, Slug: "Kitchen"} },
	}
	info := d.Info(context.Background(), nil)
	want := []string{"http://192.168.1.5:8088", "http://Studio.local:8088", "https://kitchen.relay.maktaba.tv"}
	if !reflect.DeepEqual(info.URLs, want) {
		t.Fatalf("urls = %v, want %v", info.URLs, want)
	}
	if info.ID != "" || info.Name != "studio" {
		t.Fatalf("before the identity: %+v", info)
	}
	d.setStore(identityStore{id: cluster.Identity{InstanceID: "0A1B2C3D-0000-4000-8000-000000000000", Name: "Kitchen Mac"}})
	info = d.Info(context.Background(), nil)
	if info.ID != "0a1b2c3d-0000-4000-8000-000000000000" || info.Name != "Kitchen Mac" || info.Version == "" {
		t.Fatalf("info = %+v", info)
	}
	d.setStore(identityStore{err: errors.New("db down")})
	if info := d.Info(context.Background(), nil); info.Name != "studio" || info.ID != "" {
		t.Fatalf("identity unreadable: %+v", info)
	}
}

func TestHomeDirectoryURLs(t *testing.T) {
	cases := []struct {
		name string
		d    *homeDirectory
		want []string
	}{
		{"nothing known", &homeDirectory{}, nil},
		{"loopback web address: only the .local one",
			&homeDirectory{WebURL: "http://127.0.0.1:8088", LocalHost: "studio.local"}, []string{"http://studio.local:8088"}},
		{"a base path is kept", &homeDirectory{WebURL: "https://home.example/maktaba/"},
			[]string{"https://home.example/maktaba"}},
		{"another relay", &homeDirectory{RelayWS: "wss://relay.example.org/v1/relay/ws",
			Cloud: func() subh.CloudLink { return subh.CloudLink{Claimed: true, Slug: "den"} }},
			[]string{"https://den.relay.example.org"}},
		{"not claimed", &homeDirectory{Cloud: func() subh.CloudLink { return subh.CloudLink{Slug: "den"} }}, nil},
		{"a slug that is not a label", &homeDirectory{
			Cloud: func() subh.CloudLink { return subh.CloudLink{Claimed: true, Slug: "evil.com/x"} }}, nil},
	}
	for _, c := range cases {
		if got := c.d.urls(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: urls = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWebBasePath(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "http://192.168.1.5:8088": "", "http://h:8088/": "", "https://h/maktaba/": "/maktaba",
	} {
		if got := webBasePath(in); got != want {
			t.Errorf("webBasePath(%q) = %q, want %q", in, got, want)
		}
	}
}
