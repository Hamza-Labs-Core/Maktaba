package main

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relaydns"
)

// The command's settings: the token only from its file, the domain's two names, the defaults.
func TestLoad(t *testing.T) {
	envs := map[string]string{"RELAY_DNS_DB_URL": "postgres://w", "CF_ZONE_ID": "z1", "CF_API_TOKEN_FILE": "/run/cf"}
	getenv := func(k string) string { return envs[k] }
	files := map[string]string{"/run/cf": "tok\n"}
	read := func(p string) ([]byte, error) {
		if v, ok := files[p]; ok {
			return []byte(v), nil
		}
		return nil, errors.New("no file")
	}
	s, err := load(getenv, read)
	if err != nil {
		t.Fatal(err)
	}
	if s.token != "tok" || s.domain != "relay.maktaba.tv" || s.every.Seconds() != 15 || s.port != 443 || s.once {
		t.Fatalf("settings %+v", s)
	}
	c := s.controller(relay.NewMemPoolStore(), slog.New(slog.DiscardHandler))
	if strings.Join(c.Names, ",") != "relay.maktaba.tv,*.relay.maktaba.tv" || c.TTL != 60 || c.Probe == nil {
		t.Fatalf("controller %+v", c)
	}
	if cf := c.DNS.(*relaydns.Cloudflare); cf.ZoneID != "z1" || cf.Token != "tok" {
		t.Fatalf("cloudflare %+v", cf)
	}
	for name, mut := range map[string]func(){
		"no db":         func() { delete(envs, "RELAY_DNS_DB_URL") },
		"no zone":       func() { delete(envs, "CF_ZONE_ID") },
		"no token file": func() { delete(envs, "CF_API_TOKEN_FILE") },
		"missing file":  func() { envs["CF_API_TOKEN_FILE"] = "/nope" },
		"empty token":   func() { files["/run/cf"] = " \n" },
		"bad every":     func() { envs["RELAY_DNS_EVERY"] = "soon" },
		"bad port":      func() { envs["RELAY_DNS_PROBE_PORT"] = "0" },
	} {
		saved, savedFiles := map[string]string{}, map[string]string{}
		for k, v := range envs {
			saved[k] = v
		}
		for k, v := range files {
			savedFiles[k] = v
		}
		mut()
		if _, err := load(getenv, read); err == nil {
			t.Errorf("%s: accepted", name)
		}
		envs, files = saved, savedFiles
	}
	// the token in the environment is not read
	envs["CF_API_TOKEN"] = "leak"
	delete(envs, "CF_API_TOKEN_FILE")
	if _, err := load(getenv, read); err == nil {
		t.Error("a token from the environment was accepted")
	}
}
