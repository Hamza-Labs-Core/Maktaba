package main

import (
	"reflect"
	"sort"
	"testing"
)

func TestPyCallLines(t *testing.T) {
	src := `import os

X = compute()


@decorator
def f(a):
    return a + 1


class K:
    attr = 1

    def m(self):
        if self:
            return 2

        return 3


async def g():
    await f(1)
Y = 2
`
	got := pyCallLines([]byte(src))
	want := map[int]bool{8: true, 15: true, 16: true, 18: true, 22: true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pyCallLines = %v, want %v", sortedInts(got), sortedInts(want))
	}
}

func sortedInts(m map[int]bool) []int {
	var out []int
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func TestReportName(t *testing.T) {
	for in, want := range map[string]string{
		"build/coverage-fragment/player-mac.xccov.json": "player-mac",
		"x/core.jacoco.xml":                             "core",
		"kit.json":                                      "kit",
	} {
		if got := reportName(in); got != want {
			t.Errorf("reportName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlanUsesTheDefaultAndroidSDK(t *testing.T) {
	const f = "apps/android/core/src/test/kotlin/CoreTest.kt"
	repo := memRepo{f: "package com.x.core\n"}
	a := &Analysis{Hits: map[string]*Hit{f: {File: f, Platform: PAndroid, Via: map[string]bool{ViaNaming: true}}}}
	env := Env{GOOS: "darwin", Has: func(string) bool { return true }, Exists: func(string) bool { return false },
		Getenv: func(string) string { return "" }, JavaHome: "/jdk", SDK: "/sdk"}
	s := plan(a, repo, env)[0]
	if s.Skip != "" || !contains(s.Env, "ANDROID_HOME=/sdk") {
		t.Errorf("step = %+v", s)
	}
	env.SDK = ""
	if s := plan(a, repo, env)[0]; s.Skip == "" {
		t.Errorf("no SDK anywhere, not skipped: %+v", s)
	}
}
