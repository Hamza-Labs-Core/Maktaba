# Implementation Plan — Story 20.8 Flaky Test Policy

> Companion to [story-20-08-flaky-test-policy.md](story-20-08-flaky-test-policy.md).
> Flake registry, auto-quarantine at 3-in-7-days, 14-day SLA, retries gated to e2e tier only.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Registry | `tools/flakes/registry.json` committed; updated only by CI bot. |
| Auto-quarantine | Workflow runs nightly; tests with ≥ 3 flakes in 7 days are added to `tools/flakes/quarantined.txt`. Test runner reads the list and emits `t.Skip("quarantined: #ISSUE")`. |
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

var quarantined map[string]string  // TestName → "#1234"

func init() {
    f, err := os.Open(filepath.Join(repoRoot(), "tools/flakes/quarantined.txt"))
    if err != nil { return }
    defer f.Close()
    quarantined = map[string]string{}
    sc := bufio.NewScanner(f)
    for sc.Scan() {
        parts := strings.Fields(sc.Text())
        if len(parts) >= 2 { quarantined[parts[0]] = parts[1] }
    }
}

// Helper used by every test file's TestMain via go:linkname or an explicit
// SkipIfQuarantined(t) call at the top of t.Run blocks.
func SkipIfQuarantined(t *testing.T) {
    if iss, ok := quarantined[t.Name()]; ok {
        t.Skipf("quarantined-flake: %s", iss)
    }
}
```

Convention: every test file calls `flakes.SkipIfQuarantined(t)` as the first statement. A lint enforces the call exists in tagged tests.

## 6. SLA check (14 days)

```go
// tools/flakes/sla_check.go
func main() {
    reg := loadRegistry("tools/flakes/registry.json")
    deadline := time.Now().AddDate(0, 0, -14)
    for _, t := range reg.Tests {
        if t.QuarantinedAt.IsZero() { continue }
        if t.QuarantinedAt.After(deadline) { continue }
        // SLA breach
        gh.IssueComment(t.Issue, "🚨 14-day quarantine SLA breached. @"+t.Owner+" please fix or delete.")
        // Page via existing alerting (Story 21.5)
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
| EC1 infra hiccup | story | Recorder classifies failures: `flake_category=infra` (DNS, container start) is excluded from flake budget. Categories detected via regex on logs. |
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
  infra_categories:
    - "container start failed"
    - "i/o timeout"
    - "no such host"
```

## 12. Dashboards

`docs/dashboards/flakes.html` reads `tools/flakes/registry.json` from the default branch and lists active flakes, flap counts, owners, SLAs.

## 13. Dependencies

- Story 20.1 (tier definitions + retry policy boundary).
- Story 21.5 (alerting on SLA breach).
- Epic 22 (CI workflows; on-call paging).
