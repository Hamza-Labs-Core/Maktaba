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
	policy := PolicyFromResolver("api.maktaba.app,admin.maktaba.app", "relay.maktaba.app", r)

	cases := []struct {
		host string
		want bool
	}{
		{"api.maktaba.app", true},
		{"admin.maktaba.app", true},
		{"abc.relay.maktaba.app", true},
		{"xyz.relay.maktaba.app", false},
		{"abc.def.relay.maktaba.app", false},
		{"evil.example.com", false},
		{".relay.maktaba.app", false},
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
