//go:build rehearsalbad

package main

// The cloud rehearsal's bad release (Epic 50 story 50.08, scenario
// deploy-bad-release-rollback): a build whose relay passes its readiness at first
// and fails it 20 s after it starts, the way a release with a slow-burning fault
// would. Never published: only tools/rehearsal-cloud/Dockerfile builds it, with
// BUILD_TAGS=rehearsalbad.
func init() { badAfter = 20 }
