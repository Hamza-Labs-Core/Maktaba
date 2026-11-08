package config

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// Epic 50 story 50.32 §1: the admin listener's settings parse, and the listener and its name
// come together.
func TestAdminListenerSettings(t *testing.T) {
	c, err := loadTOML(t, `
[server]
listen_addr = "127.0.0.1:8090"
admin_listen_addr = "127.0.0.1:8091"
admin_host = "Admin.Maktaba.TV"
admin_origin = "http://localhost:5175/"
user_hosts = ["api.maktaba.tv", "App.maktaba.tv"]
`)
	if err != nil {
		t.Fatal(err)
	}
	s := c.Server
	if s.AdminHost != "admin.maktaba.tv" || s.AdminOrigin != "http://localhost:5175" ||
		!slices.Equal(s.UserHosts, []string{"api.maktaba.tv", "app.maktaba.tv"}) {
		t.Fatalf("parsed %+v", s)
	}
	if s.AdminOriginOrDefault() != "http://localhost:5175" {
		t.Errorf("AdminOriginOrDefault = %q", s.AdminOriginOrDefault())
	}
	s.AdminOrigin = ""
	if s.AdminOriginOrDefault() != "https://admin.maktaba.tv" {
		t.Errorf("the default origin = %q", s.AdminOriginOrDefault())
	}
}

func TestAdminListenerValidation(t *testing.T) {
	base := func() Config {
		c, _ := Load("")
		c.Database.URL = "postgres://x"
		c.Server.ListenAddr = "127.0.0.1:8090"
		return c
	}
	for _, tc := range []struct {
		name   string
		adjust func(*ServerConfig)
		want   string // "" = valid
	}{
		{"none at all", func(*ServerConfig) {}, ""},
		{"both", func(s *ServerConfig) { s.AdminListenAddr, s.AdminHost = "127.0.0.1:8091", "admin.maktaba.tv" }, ""},
		{"a listener without its name", func(s *ServerConfig) { s.AdminListenAddr = "127.0.0.1:8091" }, "server.admin_host"},
		{"a name without a listener", func(s *ServerConfig) { s.AdminHost = "admin.maktaba.tv" }, "server.admin_listen_addr"},
		{"the user listener's address", func(s *ServerConfig) {
			s.AdminListenAddr, s.AdminHost = "127.0.0.1:8090", "admin.maktaba.tv"
		}, "its own"},
		{"ephemeral ports (tests)", func(s *ServerConfig) {
			s.ListenAddr, s.AdminListenAddr, s.AdminHost = "127.0.0.1:0", "127.0.0.1:0", "admin.maktaba.tv"
		}, ""},
		{"an origin with a path", func(s *ServerConfig) {
			s.AdminListenAddr, s.AdminHost, s.AdminOrigin = "127.0.0.1:8091", "admin.maktaba.tv", "https://admin.maktaba.tv/x"
		}, "not an origin"},
		{"an origin without a scheme", func(s *ServerConfig) {
			s.AdminListenAddr, s.AdminHost, s.AdminOrigin = "127.0.0.1:8091", "admin.maktaba.tv", "admin.maktaba.tv"
		}, "not an origin"},
	} {
		c := base()
		tc.adjust(&c.Server)
		err := Validate(c, RoleAPI)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
		if tc.want == "server.admin_host" && !errors.Is(err, ErrMissing) {
			t.Errorf("%s: not ErrMissing", tc.name)
		}
	}
}
