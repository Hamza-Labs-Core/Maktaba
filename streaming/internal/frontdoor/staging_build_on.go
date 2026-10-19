//go:build tvstaging

package frontdoor

// StagingBuild: built with the `tvstaging` tag, rules.tv_staging.enabled
// (default on) lets the change controller start a staging slot.
const StagingBuild = true
