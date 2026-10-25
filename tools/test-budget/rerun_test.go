package main

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// fakeGoTest stands in for `go test`: it records each re-run and answers
// with the canned -json output for its -run pattern.
type fakeGoTest struct {
	t       *testing.T
	replies map[string]string // -run pattern -> output
	err     error
	calls   [][]string
	dirs    []string
}

func (f *fakeGoTest) run(dir string, argv []string) ([]byte, error) {
	f.calls = append(f.calls, argv)
	f.dirs = append(f.dirs, dir)
	pattern := argv[len(argv)-2]
	reply, ok := f.replies[pattern]
	if !ok {
		f.t.Errorf("unexpected re-run %q", argv)
	}
	return []byte(reply), f.err
}

func (f *fakeGoTest) patterns() []string {
	p := []string{}
	for _, c := range f.calls {
		p = append(p, c[len(c)-2])
	}
	return p
}

var unitGoTest = []string{"go", "test", "-short", "-race", "-count=1", "-json"}

// rerunCfg is unitCfg (hard cap 300 ms) re-running breaches through f.
func rerunCfg(f *fakeGoTest) *config {
	cfg := unitCfg()
	cfg.rerunCmd = unitGoTest
	cfg.rerunDir = "/repo/mod"
	cfg.rerunMax = defaultRerunMax
	cfg.run = f.run
	return cfg
}

// slowStream is a package whose one test passed in 1.24 s, past the 300
// ms hard cap.
const slowStream = `
{"Action":"run","Package":"p","Test":"TestSlow"}
{"Action":"output","Package":"p","Test":"TestSlow","Output":"=== RUN   TestSlow\n"}
{"Action":"pass","Package":"p","Test":"TestSlow","Elapsed":1.24}
{"Action":"pass","Package":"p","Elapsed":1.3}
`

// A breach that does not repeat alone is a warning naming both timings,
// and the run passes. The re-run is the same go test command, alone, in
// the module, anchored to exactly that test.
func TestRunJSONBreachThatDoesNotRepeatWarns(t *testing.T) {
	f := &fakeGoTest{t: t, replies: map[string]string{"^TestSlow$": `
{"Action":"start","Package":"p"}
{"Action":"run","Package":"p","Test":"TestSlow"}
{"Action":"pass","Package":"p","Test":"TestSlow","Elapsed":0.05}
{"Action":"pass","Package":"p","Elapsed":0.06}
`}}
	var out bytes.Buffer
	if got := runJSON(rerunCfg(f), strings.NewReader(slowStream), &out); got != 0 {
		t.Fatalf("a breach that does not repeat must pass, got exit %d:\n%s", got, out.String())
	}
	want := append(slices.Clone(unitGoTest), "-p", "1", "-run", "^TestSlow$", "p")
	if len(f.calls) != 1 || !slices.Equal(f.calls[0], want) || f.dirs[0] != "/repo/mod" {
		t.Fatalf("re-runs = %q in %q, want one %q in /repo/mod", f.calls, f.dirs, want)
	}
	s := out.String()
	for _, w := range []string{
		"::warning::test-budget [unit] p::TestSlow took 1.24s > 3x soft cap 100ms",
		"50ms re-run alone",
	} {
		if !strings.Contains(s, w) {
			t.Fatalf("missing %q in:\n%s", w, s)
		}
	}
	if strings.Contains(s, "::error::") {
		t.Fatalf("a breach that does not repeat reported as an error:\n%s", s)
	}
}

// A breach that repeats alone fails the build, naming both timings.
func TestRunJSONBreachThatRepeatsFails(t *testing.T) {
	f := &fakeGoTest{t: t, replies: map[string]string{"^TestSlow$": `
{"Action":"run","Package":"p","Test":"TestSlow"}
{"Action":"pass","Package":"p","Test":"TestSlow","Elapsed":0.9}
{"Action":"pass","Package":"p","Elapsed":0.91}
`}}
	var out bytes.Buffer
	if got := runJSON(rerunCfg(f), strings.NewReader(slowStream), &out); got != 1 {
		t.Fatalf("a breach that repeats must fail, got exit %d:\n%s", got, out.String())
	}
	s := out.String()
	for _, w := range []string{
		"::error::test-budget [unit] 1 hard soft-cap breach(es):",
		"  p::TestSlow took 1.24s > 3x soft cap 100ms; re-run alone: 900ms",
	} {
		if !strings.Contains(s, w) {
			t.Fatalf("missing %q in:\n%s", w, s)
		}
	}
	if strings.Contains(s, "::warning::") {
		t.Fatalf("a breach that repeats reported as a warning:\n%s", s)
	}
}

// A test that fails when re-run alone fails the build, with what it
// printed; one the re-run never reports (a build error) too, with the
// re-run's own output.
func TestRunJSONBreachWhoseRerunFailsOrDoesNotReport(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		err         error
		want        []string
	}{
		{
			name: "fails alone",
			reply: `
{"Action":"run","Package":"p","Test":"TestSlow"}
{"Action":"output","Package":"p","Test":"TestSlow","Output":"    slow_test.go:12: order-dependent: boom\n"}
{"Action":"fail","Package":"p","Test":"TestSlow","Elapsed":0.01}
{"Action":"fail","Package":"p","Elapsed":0.02}
`,
			err:  errors.New("exit status 1"),
			want: []string{"p::TestSlow took 1.24s > 3x soft cap 100ms; re-run alone: failed", "slow_test.go:12: order-dependent: boom"},
		},
		{
			name: "does not report",
			reply: `
{"ImportPath":"p [p.test]","Action":"build-output","Output":"p/x.go:3:1: syntax error\n"}
{"ImportPath":"p [p.test]","Action":"build-fail"}
go: some go error
`,
			err:  errors.New("exit status 1"),
			want: []string{"p::TestSlow took 1.24s > 3x soft cap 100ms; re-run alone: not reported (exit status 1)", "p/x.go:3:1: syntax error", "go: some go error"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeGoTest{t: t, replies: map[string]string{"^TestSlow$": tc.reply}, err: tc.err}
			var out bytes.Buffer
			if got := runJSON(rerunCfg(f), strings.NewReader(slowStream), &out); got != 1 {
				t.Fatalf("got exit %d, want 1:\n%s", got, out.String())
			}
			for _, w := range tc.want {
				if !strings.Contains(out.String(), w) {
					t.Fatalf("missing %q in:\n%s", w, out.String())
				}
			}
		})
	}
}

// The -run pattern selects exactly the breaching test: each level quoted
// and anchored, spaces as go test rewrites them.
func TestRunPattern(t *testing.T) {
	for _, tc := range []struct {
		test, want string
		nearMiss   []string // names the pattern must not select
	}{
		{"TestX", `^TestX$`, []string{"TestXY", "ATestX"}},
		{"TestX/case_a", `^TestX$/^case_a$`, []string{"TestX/case_ab", "TestXY/case_a"}},
		{"TestX/with space", `^TestX$/^with_space$`, []string{"TestX/with_spaces"}},
		{"TestX/a.b(c)|d[e]+", `^TestX$/^a\.b\(c\)\|d\[e\]\+$`, []string{"TestX/aXb(c)|d[e]+", "TestX/a.b(c)"}},
		{"TestX/outer/inner#01", `^TestX$/^outer$/^inner#01$`, []string{"TestX/outer/inner", "TestX/outer2/inner#01"}},
	} {
		got := runPattern(tc.test)
		if got != tc.want {
			t.Errorf("runPattern(%q) = %q, want %q", tc.test, got, tc.want)
			continue
		}
		name := strings.ReplaceAll(tc.test, " ", "_") // as go test names it
		if !selects(got, name) {
			t.Errorf("%q does not select %q", got, name)
		}
		for _, m := range tc.nearMiss {
			if selects(got, m) {
				t.Errorf("%q selects %q too", got, m)
			}
		}
	}
}

// selects reports whether go test -run pattern runs the test named name:
// each /-separated level of the pattern must match the same level of the
// name (the pattern has no / inside brackets or parentheses).
func selects(pattern, name string) bool {
	pl, nl := strings.Split(pattern, "/"), strings.Split(name, "/")
	if len(pl) != len(nl) {
		return false
	}
	for i := range pl {
		if !regexp.MustCompile(pl[i]).MatchString(nl[i]) {
			return false
		}
	}
	return true
}

// A breaching subtest whose parent breached too rides on the parent's
// re-run; a parallel subtest (its time is not its parent's) is re-run on
// its own, by its full name.
func TestRunJSONSubtestBreaches(t *testing.T) {
	in := strings.NewReader(`
{"Action":"run","Package":"p","Test":"TestP"}
{"Action":"run","Package":"p","Test":"TestP/slow_case"}
{"Action":"pass","Package":"p","Test":"TestP/slow_case","Elapsed":1.0}
{"Action":"pass","Package":"p","Test":"TestP","Elapsed":1.1}
{"Action":"run","Package":"p","Test":"TestQ"}
{"Action":"run","Package":"p","Test":"TestQ/par.1"}
{"Action":"pass","Package":"p","Test":"TestQ/par.1","Elapsed":0.5}
{"Action":"pass","Package":"p","Test":"TestQ","Elapsed":0.01}
{"Action":"pass","Package":"p","Elapsed":1.7}
`)
	f := &fakeGoTest{t: t, replies: map[string]string{
		`^TestP$`: `
{"Action":"pass","Package":"p","Test":"TestP/slow_case","Elapsed":0.9}
{"Action":"pass","Package":"p","Test":"TestP","Elapsed":0.95}
`,
		`^TestQ$/^par\.1$`: `
{"Action":"pass","Package":"p","Test":"TestQ/par.1","Elapsed":0.1}
{"Action":"pass","Package":"p","Test":"TestQ","Elapsed":0.11}
`,
	}}
	var out bytes.Buffer
	if got := runJSON(rerunCfg(f), in, &out); got != 1 {
		t.Fatalf("got exit %d, want 1:\n%s", got, out.String())
	}
	if want := []string{`^TestP$`, `^TestQ$/^par\.1$`}; !slices.Equal(f.patterns(), want) {
		t.Fatalf("re-ran %q, want %q", f.patterns(), want)
	}
	s := out.String()
	for _, w := range []string{
		"::error::test-budget [unit] 2 hard soft-cap breach(es):",
		"  p::TestP/slow_case took 1s > 3x soft cap 100ms; re-run alone: 900ms",
		"  p::TestP took 1.1s > 3x soft cap 100ms; re-run alone: 950ms",
		"::warning::test-budget [unit] p::TestQ/par.1 took 500ms > 3x soft cap 100ms with the other packages running, 100ms re-run alone",
	} {
		if !strings.Contains(s, w) {
			t.Fatalf("missing %q in:\n%s", w, s)
		}
	}
}

// Up to rerunMax tests are re-run one by one. More are re-run together,
// one go test for their package with -p 1 -parallel 1 (each alone, in
// turn: a package's t.Parallel tests stretch each other under load): those
// fast alone are warnings, and a slowdown that repeats there still fails.
func TestRunJSONManyBreachesAreRerunTogether(t *testing.T) {
	for _, tc := range []struct {
		name    string
		n       int
		alone   float64 // each test's time in the re-run
		wantErr bool
	}{
		{"at the limit", defaultRerunMax, 0.01, false},
		{"over it, the runner's load", defaultRerunMax + 1, 0.01, false},
		{"over it, a slowdown", defaultRerunMax + 1, 0.5, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stream, batch strings.Builder
			f := &fakeGoTest{t: t, replies: map[string]string{}}
			names := []string{}
			for i := range tc.n {
				name := fmt.Sprintf("TestSlow%d", i)
				names = append(names, name)
				fmt.Fprintf(&stream, `{"Action":"pass","Package":"p","Test":%q,"Elapsed":0.5}`+"\n", name)
				reply := fmt.Sprintf(`{"Action":"pass","Package":"p","Test":%q,"Elapsed":%g}`+"\n", name, tc.alone)
				f.replies["^"+name+"$"] = reply
				batch.WriteString(reply)
			}
			stream.WriteString(`{"Action":"pass","Package":"p","Elapsed":6}` + "\n")
			together := "^(" + strings.Join(names, "|") + ")$"
			if tc.n > defaultRerunMax {
				f.replies = map[string]string{together: batch.String()}
			}
			var out bytes.Buffer
			got := runJSON(rerunCfg(f), strings.NewReader(stream.String()), &out)
			s := out.String()
			if tc.n <= defaultRerunMax {
				if got != 0 || len(f.calls) != tc.n || strings.Count(s, "::warning::") != tc.n {
					t.Fatalf("%d breaches: exit %d, %d re-runs, want 0 and %d warnings:\n%s", tc.n, got, len(f.calls), tc.n, s)
				}
				return
			}
			want := append(slices.Clone(unitGoTest), "-p", "1", "-parallel", "1", "-run", together, "p")
			if len(f.calls) != 1 || !slices.Equal(f.calls[0], want) {
				t.Fatalf("re-runs = %q, want one %q", f.calls, want)
			}
			if !strings.Contains(s, fmt.Sprintf("%d tests past the hard cap 300ms, more than %d: re-run together", tc.n, defaultRerunMax)) {
				t.Fatalf("no batch line in:\n%s", s)
			}
			if tc.wantErr {
				if got != 1 || !strings.Contains(s, fmt.Sprintf("::error::test-budget [unit] %d hard soft-cap breach(es):", tc.n)) {
					t.Fatalf("a slowdown that repeats alone must fail, got exit %d:\n%s", got, s)
				}
				return
			}
			if got != 0 || strings.Count(s, "::warning::") != tc.n {
				t.Fatalf("breaches fast alone: exit %d, want 0 and %d warnings:\n%s", got, tc.n, s)
			}
		})
	}
}

// A test that failed is not re-run: go test fails the build, and its
// breach stands as it did before re-runs.
func TestRunJSONFailedTestBreachIsNotRerun(t *testing.T) {
	in := strings.NewReader(`
{"Action":"run","Package":"p","Test":"TestBad"}
{"Action":"output","Package":"p","Test":"TestBad","Output":"    bad_test.go:9: want 1, got 2\n"}
{"Action":"fail","Package":"p","Test":"TestBad","Elapsed":1.0}
{"Action":"fail","Package":"p","Elapsed":1.1}
`)
	f := &fakeGoTest{t: t}
	var out bytes.Buffer
	if got := runJSON(rerunCfg(f), in, &out); got != 1 || len(f.calls) != 0 {
		t.Fatalf("exit %d after %d re-runs, want 1 and none:\n%s", got, len(f.calls), out.String())
	}
	s := out.String()
	for _, w := range []string{"--- FAIL p::TestBad", "bad_test.go:9: want 1, got 2", "p::TestBad took 1s > 3x soft cap 100ms (the test failed: not re-run)"} {
		if !strings.Contains(s, w) {
			t.Fatalf("missing %q in:\n%s", w, s)
		}
	}
}
