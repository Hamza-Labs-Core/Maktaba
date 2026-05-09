// Package testtier is the shared test-tier toolkit for Maktaba's Go
// modules (Story 20.1). It provides three things:
//
//  1. Per-tier soft-cap helpers (WithUnitSoftCap / WithIntegrationSoftCap
//     / WithE2ESoftCap) that warn on slow tests and fail on egregiously
//     slow tests (>3× the soft cap). The caps come from AC4 of Story
//     20.1 and are exported as constants so the budget enforcer in
//     tools/test-budget can read the same values.
//
//  2. A unit-tier network guard (EnableUnitNetGuard) that swaps
//     net.DefaultResolver for one whose Dial hook always returns an
//     error. Unit tests must not do I/O (AC1). Tests that wire the
//     guard via TestMain catch accidental network calls inside the
//     `go test -short` tier.
//
//  3. A tmp-leak sweep (AssertNoTmpLeaks) integration tests can call
//     from TestMain to enforce EC3 — every integration test must own a
//     t.TempDir() and leave nothing behind in /tmp/maktaba-*.
//
// Tier separation in the Go modules is driven by `go test -short` for
// the unit tier and `//go:build integration` for the integration tier
// (Makefile contract). Tests that need to skip when the wrong tier is
// active should call RequireUnit / RequireIntegration.
package testtier
