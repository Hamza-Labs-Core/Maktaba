package slot

import (
	"go/build"
	"strings"
	"testing"
)

// TestVerifyNeverPushes is story 33.5 TC6 / AC8: a slot (live, standby or
// staging) has no push code path — neither the slot package nor the channel
// engine it runs imports the push relay or the remote-viewing code, so a
// staging slot can never open a Cloudflare input or ask for push credentials
// (tv_push_credentials answers the front door only).
func TestVerifyNeverPushes(t *testing.T) {
	for _, pkg := range []string{".", "../channel"} {
		p, err := build.ImportDir(pkg, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range p.Imports {
			if strings.Contains(imp, "/frontdoor") || strings.Contains(imp, "/relay") || strings.Contains(imp, "remote") {
				t.Errorf("%s imports %s: a slot must have no push path", p.ImportPath, imp)
			}
		}
	}
}
