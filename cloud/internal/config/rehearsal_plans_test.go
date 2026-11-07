package config

import "testing"

// The relay-plans rehearsal's config (story 50.19 T13) parses and enforces the plans.
func TestTheRehearsalPlansConfig(t *testing.T) {
	c, err := Load("../../../tools/rehearsal-cloud/relay-plans.toml")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Relay.PlansEnforce || !c.Relay.StreamLimitEnabled {
		t.Fatalf("%+v", c.Relay)
	}
}
