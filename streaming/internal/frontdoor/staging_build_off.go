//go:build !tvstaging

package frontdoor

// StagingBuild: staging (stories 33.5-33.9) is compiled in by the `tvstaging`
// build tag, which every release and `make build-go` set (STREAMING_TAGS).
// A build without it (plain `go build`, STREAMING_TAGS=) ignores
// rules.tv_staging.enabled and never starts a staging slot; the live slot and
// the hot standby run either way.
const StagingBuild = false
