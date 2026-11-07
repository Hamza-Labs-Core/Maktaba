package config

import "testing"

// The example configuration parses, [direct] and its CAs included (story 50.15).
func TestTheExampleConfigParses(t *testing.T) {
	c, err := Load("../../configs/cloud.example.toml")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Direct.Domains) != 3 || c.Direct.Enabled || len(c.Direct.ACME) != 3 {
		t.Fatalf("%+v", c.Direct)
	}
}
