package tls

import (
	"context"
	"errors"
	"testing"
)

type fakeResolver struct {
	known map[string]bool
}

func (f *fakeResolver) HasSlug(_ context.Context, slug string) (bool, error) {
	return f.known[slug], nil
}

func TestPolicyFromResolver(t *testing.T) {
	r := &fakeResolver{known: map[string]bool{"abc": true}}
	policy := PolicyFromResolver("api.maktaba.tv,admin.maktaba.tv", "relay.maktaba.tv", r)

	cases := []struct {
		host string
		want bool
	}{
		{"api.maktaba.tv", true},
		{"admin.maktaba.tv", true},
		{"abc.relay.maktaba.tv", true},
		{"xyz.relay.maktaba.tv", false},
		{"abc.def.relay.maktaba.tv", false},
		{"evil.example.com", false},
		{".relay.maktaba.tv", false},
	}
	for _, c := range cases {
		err := policy(context.Background(), c.host)
		got := err == nil
		if got != c.want {
			t.Errorf("policy(%q) ok=%v want=%v (err=%v)", c.host, got, c.want, err)
		}
	}
	_ = errors.New
}

func TestPolicyRefusesUnknownAndNested(t *testing.T) {
	p := PolicyFromResolver("relay.test", "relay.test", &fakeResolver{known: map[string]bool{"home": true}})
	for host, ok := range map[string]bool{
		"relay.test": true, "home.relay.test": true,
		"other.relay.test": false, "a.home.relay.test": false, "evil.test": false, "home.relay.test.evil": false,
	} {
		if err := p(context.Background(), host); (err == nil) != ok {
			t.Errorf("%s: err=%v, want ok=%v", host, err, ok)
		}
	}
}
