package config

import "testing"

func TestPublicStreamURL(t *testing.T) {
	old := lanIP
	lanIP = func() string { return "192.168.1.20" }
	t.Cleanup(func() { lanIP = old })

	cases := []struct {
		stream, public, want string
	}{
		{":8081", "", "http://192.168.1.20:8081"},
		{"0.0.0.0:8782", "", "http://192.168.1.20:8782"},
		{"10.0.0.5:9000", "", "http://10.0.0.5:9000"},
		{"garbage", "", "http://192.168.1.20:8081"},
		{":8081", "http://tv.lan:8081/", "http://tv.lan:8081"},
		{":8081", "tv.lan:8081", "http://tv.lan:8081"},
	}
	for _, c := range cases {
		cfg := Defaults()
		cfg.Server.StreamAddr, cfg.Server.PublicURL = c.stream, c.public
		if got := cfg.PublicStreamURL(); got != c.want {
			t.Errorf("stream=%q public=%q: got %q want %q", c.stream, c.public, got, c.want)
		}
	}
}

func TestPublicURLLoadsFromTOML(t *testing.T) {
	p := t.TempDir() + "/server.toml"
	if err := writeFile(p, "[server]\npublic_url = \"http://tv.lan:8782\"\n"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadFrom(p)
	if err != nil || cfg.Server.PublicURL != "http://tv.lan:8782" {
		t.Fatalf("public_url = %q (%v)", cfg.Server.PublicURL, err)
	}
	if cfg.Server.StreamAddr != ":8081" {
		t.Fatalf("defaults lost: %q", cfg.Server.StreamAddr)
	}
}
