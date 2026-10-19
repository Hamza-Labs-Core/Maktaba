//go:build !tvstaging

package frontdoor

// StagingBuild: staging (stories 33.5-33.9) ships behind a build flag until the
// change management of Phase C lands (README decisions, Rollout): without the
// `tvstaging` build tag rules.tv_staging.enabled has no effect and no staging
// slot is ever started; the live slot and the hot standby run either way.
const StagingBuild = false
