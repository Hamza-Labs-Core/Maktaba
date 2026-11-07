// Package logbench is the log path's performance budget (Epic 53 story 53.13): the
// keeper's capture under a burst, the index's lag and catch-up, search over a seeded
// corpus, the dashboards' 30 days, the Go logger's cost per call and the disk caps. The
// benchmarks are tests behind the logbench build tag (go test -tags logbench), run by CI's
// log-bench job on a change to the log path; each fails with its numbers when over budget.
//
// The loads are scaled to a CI runner's minutes (LOGBENCH_SCALE, 1 by default: the burst is
// 10 s instead of 60, the corpus 300,000 lines instead of 15 M); the budgets are the story's,
// per line or per query, multiplied by the runner's speed against the reference machine
// (Calibrate), as test-budget does. docs/performance/logs.md has the targets and how to run
// the full loads.
package logbench
