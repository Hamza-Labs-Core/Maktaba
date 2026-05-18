//go:build race

package cloudlink

// raceEnabled is true when the test binary is built with -race. Used to
// skip a cross-component test that depends on a pre-existing CLOUD
// relay defect (cloud/internal/relay/tunnel.go:115 stream-delete race,
// Story 25.8/25.9) which is out of scope for the Epic 25 cloudlink
// client. The client's own behaviour is fully covered by deterministic
// tests in any build mode.
const raceEnabled = true
