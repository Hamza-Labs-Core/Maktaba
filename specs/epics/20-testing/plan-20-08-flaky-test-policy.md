# Implementation Plan — Story 20.8 Flaky Test Policy

> Companion to [story-20-08-flaky-test-policy.md](story-20-08-flaky-test-policy.md).
> Flake registry, auto-quarantine at 3-in-7-days, 14-day SLA, retries gated to e2e tier only.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Registry | `tools/flakes/registry.json` committed; updated only by CI bot. |
| Auto-quarantine | Workflow runs nightly; tests with ≥ 3 flakes in 7 days are added to `tools/flakes/quarantined.txt`. The skip helper emits the literal string the story specifies (story-20-08 AC2): `t.Skip("quarantined-flake-#" + iss)` (Go) — the message starts with the prefix `quarantined-flake-#` so log-grep tooling can identify quarantined skips uniformly. The Python equivalent is `pytest.skip(reason="quarantined-flake-#" + iss)`. |
| Retry policy | Per-tier flag; unit/integration retries=0; e2e retries=1. |
| Lints | Time-zone test ban; randomized order ban for ordering deps. |

## 1. Project layout

```
tools/flakes/
├── registry.json                    # the canonical record
├── quarantined.txt                  # one TestName per line + #issue
├── recorder.go                      # CI: parses test logs, updates registry
├── auto_quarantine.go
├── sla_check.go                     # alerts on 14d breach
├── test_skip_helper.go              # used by services
└── lint/
    ├── ban_time_now.go              # forbid time.Now() outside cmd/
    ├── ban_tz_test.go
    └── ban_test_order_dep.go        # implicit via random order

.github/workflows/
├── flake-record.yml
├── flake-quarantine.yml
└── flake-sla.yml
```

## 2. Registry schema

```json
// tools/flakes/registry.json
{
  "version": 1,
  "tests": [
    {
      "id": "api/internal/search.TestSearchEdgeQuery",
      "tier": "integration",
      "first_seen": "2026-04-12T03:14:11Z",
      "last_seen":  "2026-04-30T05:01:00Z",
      "occurrences": [
        { "at": "2026-04-12T03:14:11Z", "build_id": "ci-1234", "log": "https://gh-actions/run/1234" },
        { "at": "2026-04-21T12:01:00Z", "build_id": "ci-1342", "log": "..." }
      ],
      "issue": "https://github.com/maktaba/maktaba/issues/1234",
      "quarantined_at": "2026-04-30T05:01:00Z",
      "owner": "search-team"
    }
  ]
}
```

## 3. Recorder

```go
// tools/flakes/recorder.go
//go:build flakerec

func main() {
    junit := flag.String("junit", "", "path to JUnit XML file")
    flag.Parse()
    suite := parseJUnit(*junit)
    reg := loadRegistry("tools/flakes/registry.json")

    for _, tc := range suite.Cases {
        if !tc.RetryHappened || !tc.Passed { continue }
        // tc represents: failed first, passed on retry → flake
        reg.AddOccurrence(tc.QualifiedName(), Occurrence{
            At: time.Now().UTC(), BuildID: os.Getenv("GITHUB_RUN_ID"),
            Log: githubRunURL(),
        })
    }
    saveRegistry(reg)
}
```

CI step (only on `main`):

```yaml
# .github/workflows/flake-record.yml
on:
  workflow_run:
    workflows: ["test"]
    types: [completed]
    branches: [main]
jobs:
  record:
    runs-on: ubuntu-latest
    # Skip cancelled or skipped workflow runs — they don't represent a real
    # test outcome and would inflate the flake count if their JUnit output
    # is partial. We only consider `success` (passed on retry → flake
    # detection by the recorder) and `failure` (red-passing-on-rerun is
    # already excluded by `tc.RetryHappened && tc.Passed`).
    if: ${{ github.event.workflow_run.conclusion != 'cancelled' && github.event.workflow_run.conclusion != 'skipped' }}
    steps:
      - uses: actions/checkout@v4
      - run: gh run download ${{ github.event.workflow_run.id }} -n junit -D junit/
      - run: go run -tags=flakerec ./tools/flakes/recorder -junit junit/junit.xml
      - run: |
          if git diff --quiet tools/flakes/registry.json; then exit 0; fi
          git config user.email "ci@maktaba.local"
          git config user.name  "Maktaba CI"
          git add tools/flakes/registry.json
          git commit -m "flakes: record from $GITHUB_RUN_ID"
          git push
```

## 4. Auto-quarantine

```go
// tools/flakes/auto_quarantine.go
const (windowDays = 7; threshold = 3)

func main() {
    reg := loadRegistry("tools/flakes/registry.json")
    cutoff := time.Now().AddDate(0, 0, -windowDays)
    for i, t := range reg.Tests {
        recent := 0
        for _, o := range t.Occurrences {
            if o.At.After(cutoff) { recent++ }
        }
        if recent >= threshold && t.QuarantinedAt.IsZero() {
            iss := openIssue(t)
            reg.Tests[i].QuarantinedAt = time.Now().UTC()
            reg.Tests[i].Issue = iss
            appendQuarantined(t.ID, iss)
        }
    }
    saveRegistry(reg)
}
```

Output `tools/flakes/quarantined.txt`:

```text
api/internal/search.TestSearchEdgeQuery #1234
streaming/internal/range.TestCrossSegment #1280
```

## 5. Test-skip helper

```go
// tools/flakes/test_skip_helper.go
//go:build unit || integration || e2e

import (
    "bufio"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "testing"
)

// `init()` is banned outside <module-root>/cmd/* by plan-20-03 EC3, so
// quarantine state is loaded lazily on first use via sync.Once.
var (
    quarantinedOnce sync.Once
    quarantined     map[string]string  // TestName → "#1234"
)

func loadQuarantined() {
    quarantined = map[string]string{}
    f, err := os.Open(filepath.Join(repoRoot(), "tools/flakes/quarantined.txt"))
    if err != nil { return }
    defer f.Close()
    sc := bufio.NewScanner(f)
    for sc.Scan() {
        parts := strings.Fields(sc.Text())
        if len(parts) >= 2 { quarantined[parts[0]] = parts[1] }
    }
}

// SkipIfQuarantined is the canonical entry point. Every test file calls it
// as the first statement of every test function, and the lint in §8.3
// enforces that. The skip message format matches story-20-08 AC2:
// `quarantined-flake-#<issue>`.
func SkipIfQuarantined(t *testing.T) {
    quarantinedOnce.Do(loadQuarantined)
    if iss, ok := quarantined[t.Name()]; ok {
        // iss already starts with `#` (e.g. "#1234"). The full skip message
        // is therefore literally `quarantined-flake-#1234`, which matches
        // the AC2-specified form `t.Skip("quarantined-flake-#" + iss)`.
        t.Skip("quarantined-flake-" + iss)
    }
}
```

Convention: every test file calls `flakes.SkipIfQuarantined(t)` as the
first statement. The lint in §8.3 enforces this.

### 5.1 Convention lint — every `t.Skip` must go through `SkipIfQuarantined`

```go
// tools/flakes/lint/skip_via_helper.go
//go:build flakelint

// Walk every *_test.go and assert:
//
//   1. Every test function (`func TestXxx(t *testing.T)` and `t.Run` body)
//      either calls `flakes.SkipIfQuarantined(t)` as its first statement,
//      OR contains no `t.Skip*` call.
//   2. Every direct `t.Skip(msg)` / `t.Skipf(fmt, …)` whose first argument
//      is a string literal must start with the `quarantined-flake-#`
//      prefix. (Tests that legitimately need to skip for other reasons
//      should still go through `SkipIfQuarantined` plus a wrapper that
//      records the reason — keeping a uniform shape.)
//
// The walker uses `go/ast` and the `packages.Load` helper to resolve calls
// to the `flakes` package by import path, so a renamed import still works.

func main() {
    // … find all test files, parse with go/parser, type-check via go/types …
    // For each *ast.FuncDecl whose name starts with "Test":
    //   firstStmt := fn.Body.List[0]
    //   if !isCallTo(firstStmt, "flakes.SkipIfQuarantined", "t") {
    //       reportIfAnyTSkipExists(fn)
    //   }
    //   for each *ast.CallExpr that is a t.Skip / t.Skipf:
    //       if literalStringArg && !strings.HasPrefix(arg, "quarantined-flake-#") {
    //           fail("t.Skip without quarantined-flake-# prefix at "+pos)
    //       }
}
```

The lint runs in `make lint:flake-skip`; failures block the PR.

## 6. SLA check (14 days)

```go
// tools/flakes/sla_check.go
import (
    "time"

    "github.com/maktaba/maktaba/internal/alerting"   // pager hook (Story 21.5)
    "github.com/maktaba/maktaba/internal/gh"         // GitHub-issues helper
)

func main() {
    reg := loadRegistry("tools/flakes/registry.json")
    deadline := time.Now().AddDate(0, 0, -14)
    for _, t := range reg.Tests {
        if t.QuarantinedAt.IsZero() { continue }
        if t.QuarantinedAt.After(deadline) { continue }
        // SLA breach
        gh.IssueComment(t.Issue, "14-day quarantine SLA breached. @"+t.Owner+" please fix or delete.")
        // Page via existing alerting (Story 21.5).
        alerting.Page("flake-sla-breach", t.ID, t.Owner)
    }
}
```

## 7. Per-tier retry policy

```yaml
# .github/workflows/test.yml (excerpt)
- name: unit
  run: go test -tags=unit -count=1 ./...   # AC4 no retry
- name: integration
  run: pytest -m integration --no-rerun
- name: e2e
  run: pnpm playwright test --retries=1
```

## 8. Lints

### EC2 — banned time-zone tests

```go
// tools/flakes/lint/ban_tz_test.go
//go:build flakelint

func main() {
    fset := token.NewFileSet()
    bad := []string{}
    err := filepath.WalkDir(".", func(path string, d fs.DirEntry, _ error) error {
        if !strings.HasSuffix(path, "_test.go") { return nil }
        body, _ := os.ReadFile(path)
        if bytes.Contains(body, []byte("time.LoadLocation")) ||
           bytes.Contains(body, []byte("time.FixedZone")) ||
           bytes.Contains(body, []byte(`os.Setenv("TZ"`)) {
            bad = append(bad, path)
        }
        return nil
    })
    _ = err
    if len(bad) > 0 {
        fmt.Fprintf(os.Stderr, "FAIL: time-zone-dependent test(s):\n  %s\nUse the injected clock fixture.\n", strings.Join(bad, "\n  "))
        os.Exit(1)
    }
}
```

### EC3 — order-dependent ban via randomization

`go test -shuffle=on -count=1` in unit and integration tiers; pytest `-p randomly --randomly-seed=auto`. Failures expose order dependence.

## 9. Test cases

### TC1 — Synthetic flake
Add a unit test that fails 5 % of the time (e.g., `if rand.Intn(20) == 0 { t.FailNow() }`). Trigger 3 failures within 7 days (recorded by CI on main retries). Assert: nightly auto-quarantine job adds the test to `quarantined.txt`, opens an issue, and the next test run skips it.

### TC2 — SLA breach
Set a quarantined entry's `QuarantinedAt` 15 days ago. Run `make flake:sla`. Assert: GitHub comment is posted and `pageOnCall` is invoked (mocked in test).

### TC3 — Retry policy
Unit-tier failure runs once, no retry. e2e failure retries once and passes; recorded as flake.

## 10. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 infra hiccup | story | Recorder classifies failures: `flake_category=infra` (DNS, container start) is excluded from the flake budget. Detection uses **structured fields** when the runner emits JSON-formatted lines, falling back to **anchored** regex on log lines (anchored at the start of a line — `^[^\n]*`). The fallback regexes are listed in §11 `infra_categories` and each must be log-line-anchored to avoid matching the literal string inside an unrelated test name (e.g. `TestNoSuchHost` would otherwise match `no such host`). |
| EC2 timezone | story | Banned via lint; tests use `clock.Inject(t)`. |
| EC3 order dependency | story | Random shuffle on unit/integration; failures expose. |
| Recorder run race | impl | Push uses `--force-with-lease` and back-off; rejection retries up to 3×. |
| Quarantined test owner missing | impl | If owner unknown, page on-call until owner is set on the issue. |

## 11. Configuration

```yaml
flakes:
  window_days: 7
  threshold: 3
  sla_days: 14
  retries:
    unit: 0
    integration: 0
    e2e: 1
  # Preferred: classify via the structured `flake_category` field that the
  # test runner emits on each failure (JSON line). When that field is
  # missing, fall back to the anchored regexes below. Each pattern must
  # match the **start** of a log line (`^...`) — substring matches against
  # arbitrary positions produced false positives on test names that
  # happened to contain the literal phrase.
  infra_category_field: flake_category
  infra_category_fallback_regexes:
    - '^[A-Z]{1,5} +.*container start failed'
    - '^[A-Z]{1,5} +.*i/o timeout'
    - '^[A-Z]{1,5} +.*no such host'
```

## 12. Dashboards

`docs/dashboards/flakes.html` reads `tools/flakes/registry.json` from the default branch and lists active flakes, flap counts, owners, SLAs.

## 13. Dependencies

- Story 20.1 (tier definitions + retry policy boundary).
- Story 21.5 (alerting on SLA breach).
- Epic 22 (CI workflows; on-call paging).
