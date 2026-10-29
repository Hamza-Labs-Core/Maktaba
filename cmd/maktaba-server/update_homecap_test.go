package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// A joined machine's own updater never installs a release above the one its home
// committed (the owner's rule, 2026-10-28); one joined to no home updates as before.
func TestJoinedMachineNeverUpdatesPastItsHome(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	newRuntime := func(t *testing.T) runtimeDeps {
		rt := appruntime.New(filepath.Join(t.TempDir(), "runtime"))
		rt.Sync = func() {}
		if _, err := rt.Install(ctx, onePayload(t, "v0.3.41"), nil, appruntime.Options{}); err != nil {
			t.Fatal(err)
		}
		if err := rt.SetOwner(appruntime.OwnerHeadless); err != nil {
			t.Fatal(err)
		}
		return runtimeDeps{rt: rt, service: &recordingService{}}
	}

	// joined, its home committed 0.3.41: the channel's 0.3.42 is refused, nothing fetched
	d := newRuntime(t)
	d.homeCeiling = func(context.Context) (string, bool, error) { return "0.3.41", true, nil }
	src := &fakeReleases{tag: "v0.3.42", tarball: packPayload(t, onePayload(t, "v0.3.42"))}
	var out bytes.Buffer
	err := runRuntimeUpdate(ctx, &out, d, src, "", false, false)
	if err == nil || !strings.Contains(err.Error(), "never goes ahead of its home") || src.fetched != 0 {
		t.Fatalf("joined: %v (fetched %d)\n%s", err, src.fetched, out.String())
	}
	if st, _ := d.rt.Status(""); st.Version != "v0.3.41" {
		t.Fatalf("it moved: %+v", st)
	}
	// joined, its home's committed release not readable: nothing on a guess
	d.homeCeiling = func(context.Context) (string, bool, error) { return "", true, errors.New("no answer") }
	if err := runRuntimeUpdate(ctx, &out, d, src, "", false, false); err == nil || src.fetched != 0 {
		t.Fatalf("unknown ceiling: %v (fetched %d)", err, src.fetched)
	}
	// joined, the home committed 0.3.42: it goes
	d.homeCeiling = func(context.Context) (string, bool, error) { return "0.3.42", true, nil }
	if err := runRuntimeUpdate(ctx, &out, d, src, "", false, false); err != nil {
		t.Fatalf("at the home's release: %v\n%s", err, out.String())
	}

	// joined to no home: it updates as it always did
	d2 := newRuntime(t)
	d2.homeCeiling = func(context.Context) (string, bool, error) { return "", false, nil }
	src2 := &fakeReleases{tag: "v0.3.42", tarball: packPayload(t, onePayload(t, "v0.3.42"))}
	if err := runRuntimeUpdate(ctx, &out, d2, src2, "", false, false); err != nil {
		t.Fatalf("not joined: %v\n%s", err, out.String())
	}
	if st, _ := d2.rt.Status(""); st.Version != "v0.3.42" {
		t.Fatalf("not joined, not updated: %+v", st)
	}
}

// notJoined is a machine that follows no home (tests that are not about the ceiling).
func notJoined(context.Context) (string, bool, error) { return "", false, nil }

func TestHomeCeilingRules(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tag, ceiling string
		joined       bool
		err          error
		refused      bool
	}{
		{"v0.3.42", "", false, nil, false},
		{"v0.3.42", "0.3.41", true, nil, true},
		{"v0.3.42-rc.1", "0.3.41", true, nil, true},
		{"v0.3.41", "0.3.41", true, nil, false},
		{"v0.3.40", "v0.3.41", true, nil, false},
		{"v0.3.41", "", true, nil, true},
		{"v0.3.41", "0.3.41", true, errors.New("x"), true},
	} {
		if got := checkHomeCeiling(tc.tag, tc.ceiling, tc.joined, tc.err) != nil; got != tc.refused {
			t.Errorf("%+v: refused %v", tc, got)
		}
	}
}

// The ceiling is the home's /api/cluster/self `committed`; a machine with no join reads none.
func TestReadHomeCeiling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cluster/self" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"role":"home","version":"v0.3.41","committed":"0.3.41"}`))
	}))
	defer home.Close()
	nc := config.NodeConfig{DatabaseURL: "postgres://node@home/maktaba"}
	nc.Home.URL = home.URL
	if c, joined, err := readHomeCeiling(ctx, nc, home.Client()); err != nil || !joined || c != "0.3.41" {
		t.Fatalf("joined: %q %v %v", c, joined, err)
	}
	if _, joined, err := readHomeCeiling(ctx, config.NodeConfig{}, home.Client()); joined || err != nil {
		t.Fatalf("not joined: %v %v", joined, err)
	}
}
