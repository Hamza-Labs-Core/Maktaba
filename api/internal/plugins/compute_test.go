package plugins

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"testing"
)

// Story 54.21 T03: this machine and the LAN are local; the internet is not.
func TestLocalAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "10.0.0.5": true, "172.16.3.4": true, "192.168.1.20": true,
		"fd12::1": true, "169.254.10.1": true, "fe80::1": true, "::ffff:192.168.0.2": true,
		"8.8.8.8": false, "93.184.216.34": false, "2606:4700::1111": false, "100.64.0.1": false,
	} {
		if got := LocalAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: %v, want %v", addr, got, want)
		}
	}
	k := &Compute{}
	ctx := context.Background()
	for url, want := range map[string]string{
		"http://localhost:11434": "", "http://127.0.0.1:11434": "", "http://192.168.1.5:11434": "",
		"http://[::1]:11434": "", "http://8.8.8.8:11434": CodeComputeRemote, "ftp://127.0.0.1": CodeComputeRemote,
		"not a url": CodeComputeRemote,
	} {
		got := ""
		if ref := k.CheckLocal(ctx, url); ref != nil {
			got = ref.Code
		}
		if got != want {
			t.Errorf("%s: %q, want %q", url, got, want)
		}
	}
}

func TestFollowsSchema(t *testing.T) {
	var schema map[string]any
	_ = json.Unmarshal([]byte(`{"type":"object","required":["tags","n"],"properties":{
		"tags":{"type":"array","items":{"type":"string","enum":["fiqh","seerah"]}},
		"n":{"type":"integer"},"ok":{"type":"boolean"}}}`), &schema)
	for answer, want := range map[string]bool{
		`{"tags":["fiqh"],"n":2}`:                    true,
		`{"tags":["fiqh","seerah"],"n":0,"ok":true}`: true,
		`{"tags":["other"],"n":2}`:                   false,
		`{"tags":"fiqh","n":2}`:                      false,
		`{"tags":[],"n":1.5}`:                        false,
		`{"tags":[]}`:                                false,
		`{"tags":[],"n":1,"ok":"yes"}`:               false,
		`not json`:                                   false,
	} {
		if got := followsSchema(answer, schema); got != want {
			t.Errorf("%s: %v, want %v", answer, got, want)
		}
	}
}

// The defaults here equal schema.json's (pipeline plugins/compute_settings.py).
func TestComputeRulesMatchTheSchema(t *testing.T) {
	b, err := os.ReadFile("../handlers/rules/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Sections []struct {
			Key    string `json:"key"`
			Fields []struct {
				Name    string `json:"name"`
				Default any    `json:"default"`
			} `json:"fields"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	d := DefaultComputeRules()
	want := map[string]any{"url": d.URL, "model": d.Model, "generate_per_day": float64(d.GeneratePerDay),
		"embed_texts_per_day": float64(d.EmbedTextsPerDay), "timeout_s": float64(d.TimeoutS)}
	for _, s := range doc.Sections {
		if s.Key != "plugin_compute" {
			continue
		}
		if len(s.Fields) != len(want) {
			t.Fatalf("%d fields, want %d", len(s.Fields), len(want))
		}
		for _, f := range s.Fields {
			if want[f.Name] != f.Default {
				t.Errorf("%s: schema %v, Go %v", f.Name, f.Default, want[f.Name])
			}
		}
		return
	}
	t.Fatal("no plugin_compute section")
}
