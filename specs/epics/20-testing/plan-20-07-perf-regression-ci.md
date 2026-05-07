# Implementation Plan — Story 20.7 Performance Regression Tests in CI

> Companion to [story-20-07-perf-regression-ci.md](story-20-07-perf-regression-ci.md).
> Reduced perf suite per PR (≤ 5 min); full nightly; PR comment with deltas;
> auto-quarantine flapping budgets.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| `make perf-ci` | Subset of Story 18.1's harness — 9 endpoints, 3 trials each, warm only. |
| `make perf` | Full suite. |
| Storage | JSON-Lines per run pushed to `./.perf-history/{date}.jsonl` on a long-lived branch `perf-history` in the same repo (or Prometheus pushgateway if available). |
| PR comment | GitHub action posts deltas vs. `main` baseline (last green nightly). |
| Quarantine | A budget that fails 3× in 5 days on `main` is moved to `quarantined:` section of `perf_budgets.yaml`; auto-issue filed. |
| Out of scope | Cold-cache budgets (run weekly via separate target). |

## 1. Project layout

```
tests/perf/
├── ci_subset.go                    # filters budgets where ci_pr=true
├── delta_report.go                 # produces PR-comment markdown
├── publisher.go                    # writes to ./.perf-history
└── quarantine.go                   # rolling-window flap detector

.github/workflows/
├── perf-ci.yml
├── perf-nightly.yml
└── perf-quarantine.yml             # weekly housekeeping

scripts/perf/
├── post-pr-comment.sh
└── auto-file-issue.sh

docs/runbooks/
└── perf-regression.md
```

## 2. perf-ci subset

```yaml
# shared/perf_budgets.yaml (excerpt)
endpoints:
  - name: api.libraries.list
    ci_pr: true
    ...
  - name: streaming.segment.first_byte.cold
    ci_pr: false                     # full suite only
```

```go
// tests/perf/ci_subset.go
func PRSet(b *BudgetFile) []Budget {
    out := []Budget{}
    for _, e := range b.Endpoints {
        if e.CIPR && e.Cache == "warm" { out = append(out, e) }
    }
    return out
}
```

## 3. PR comment

```go
// tests/perf/delta_report.go
type Delta struct {
    Name     string
    Baseline float64
    Measured float64
    Pct      float64
    Breach   bool
    Quarantined bool
}

func Render(d []Delta) string {
    var b strings.Builder
    b.WriteString("### Perf budgets\n\n| Endpoint | Baseline p95 | This PR p95 | Δ | |\n|---|---:|---:|---:|---|\n")
    for _, x := range d {
        marker := ""
        if x.Breach { marker = "❌" }
        if x.Quarantined { marker = "🟡" }
        b.WriteString(fmt.Sprintf("| `%s` | %.1f ms | %.1f ms | %+.1f%% | %s |\n",
            x.Name, x.Baseline, x.Measured, x.Pct, marker))
    }
    return b.String()
}
```

```bash
# scripts/perf/post-pr-comment.sh
#!/usr/bin/env bash
set -euo pipefail
COMMENT=$(cat "$1")
gh pr comment "${PR_NUMBER}" --body "$COMMENT"
```

## 4. Workflow — PR

```yaml
# .github/workflows/perf-ci.yml
name: perf-ci
on: [pull_request]
jobs:
  perf:
    runs-on: [self-hosted, perf-ci, mac-m2-8gb]    # AC1 EC1 tagged runner
    timeout-minutes: 8
    steps:
      - uses: actions/checkout@v4
      - run: make perf-ci > perf-ci.json
      - run: make perf-baseline-fetch > baseline.json   # last green nightly
      - run: |
          go run ./tests/perf/cmd/render-delta \
            -baseline baseline.json -current perf-ci.json -out delta.md
      - run: scripts/perf/post-pr-comment.sh delta.md
        env: { PR_NUMBER: ${{ github.event.pull_request.number }} }
      - run: |
          # AC3: > 10 % regression on any p95 fails
          go run ./tests/perf/cmd/gate \
            -baseline baseline.json -current perf-ci.json -p95-tolerance 10
```

## 5. Workflow — nightly

```yaml
# .github/workflows/perf-nightly.yml
name: perf-nightly
on: { schedule: [{ cron: '0 5 * * *' }] }
jobs:
  perf:
    runs-on: [self-hosted, perf-ci, mac-m2-8gb]
    timeout-minutes: 45
    steps:
      - uses: actions/checkout@v4
      - run: make perf > nightly.jsonl
      - run: |
          # publish to history branch
          git fetch origin perf-history
          git worktree add /tmp/hist origin/perf-history
          mv nightly.jsonl /tmp/hist/$(date +%Y-%m-%d).jsonl
          (cd /tmp/hist && git add . && git commit -m "perf nightly $(date +%F)" && git push origin perf-history)
      - run: go run ./tests/perf/cmd/quarantine-scan history/
```

## 6. Quarantine logic

```go
// tests/perf/quarantine.go
type Window struct{ Days int; FailsThreshold int }

func ScanFlaps(history []NightlyRun, w Window) []string {
    cnt := map[string]int{}
    cutoff := time.Now().AddDate(0, 0, -w.Days)
    for _, r := range history {
        if r.At.Before(cutoff) { continue }
        for _, b := range r.Breaches { cnt[b.Name]++ }
    }
    quar := []string{}
    for name, c := range cnt {
        if c >= w.FailsThreshold { quar = append(quar, name) }
    }
    return quar
}
```

When the scanner finds new candidates, it:

1. Patches `perf_budgets.yaml` moving them to a `quarantined:` list.
2. Opens a GitHub issue `[perf-quarantine] api.search.warm` with logs attached.
3. Comments next nightly noting the quarantine.

```bash
# scripts/perf/auto-file-issue.sh
gh issue create -t "[perf-quarantine] $1" -b "Auto-quarantined after $FAILS in $WINDOW_DAYS days. See attached logs." -l perf,quarantined
```

## 7. Test cases

### TC1 — PR delta detection
PR injects a 50 ms artificial slowdown into `videos.GetByID`. `make perf-ci` captures p95 +50 ms vs baseline. Comment gets posted. Gate fires on `>10%` rule (assuming baseline is ~30 ms, +50 ms = +166 %).

### TC2 — Nightly publish
Run `perf-nightly` workflow on schedule. After the run, `perf-history` branch contains a new file `.perf-history/YYYY-MM-DD.jsonl`. Static dashboard (`scripts/perf/dashboard.html`) reads the last 30 files and renders a chart.

### TC3 — Quarantine
Inject 3 nightly fails for `api.search.warm` within 5 days. The next quarantine scan moves it to `quarantined:` and creates `[perf-quarantine] api.search.warm` issue. Subsequent runs skip its gate.

## 8. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 runner contention | story | Tagged self-hosted runners only; PR queues if no available runner. |
| EC2 cold vs warm | story | perf-ci is warm-only; weekly cold suite separate. |
| EC3 cross-OS variance | story | Per-OS reports, never averaged; baseline is per-runner-tag. |
| Baseline missing on first run after merge | impl | If no baseline, gate skips and posts "first run; no baseline". |
| Push to perf-history branch races | impl | Always git pull --rebase before push; one writer (nightly cron). |

## 9. Configuration

```yaml
perf_ci:
  pr_p95_tolerance_pct: 10
  trials_per_endpoint: 3
  warm_only: true
  baseline_branch: perf-history
  baseline_lookup_days: 7
  flap_window:
    days: 5
    fails_threshold: 3
  runner_tags:
    - mac-m2-8gb
    - linux-x86-16gb
```

## 10. Dashboard

`docs/dashboards/perf.html` — static page that fetches the last N files from `perf-history` via raw GitHub URL and plots p95 over time per endpoint with Chart.js. Linked from `docs/runbooks/perf-regression.md`.

## 11. Dependencies

- Story 18.1 (budgets file, harness).
- Story 20.1 (test tiers; perf is its own tier).
- Story 20.8 (similar quarantine model).
- Epic 22 (self-hosted runners).
