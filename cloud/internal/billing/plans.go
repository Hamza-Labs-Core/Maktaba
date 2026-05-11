// Package billing owns the plan catalog, bandwidth metering, and the
// Stripe integration glue. The catalog is hard-coded here (not in the
// DB) so the source of truth lives in version control and tier changes
// require a deploy — appropriate for the small number of public tiers.
package billing

import "time"

// Plan identifiers used across the cloud. Stored on the `users.plan`
// and `servers.plan` columns.
const (
	PlanFree   = "free"
	PlanPro    = "pro"
	PlanFamily = "family"
)

// Tier is the operator-facing description of what a Plan grants.
type Tier struct {
	ID                   string
	Name                 string
	PricePerMonthCents   int
	BandwidthBytesPerMo  int64
	MaxServers           int
	MaxConcurrentStreams int
	RelayQoS             string
	IncludesTranscoding  bool
	FamilySeats          int
}

// Tiers indexed by plan id. Values pin the spec; changes ship as a PR.
var Tiers = map[string]Tier{
	PlanFree: {
		ID:                   PlanFree,
		Name:                 "Free",
		PricePerMonthCents:   0,
		BandwidthBytesPerMo:  5 * 1024 * 1024 * 1024, // 5 GiB
		MaxServers:           1,
		MaxConcurrentStreams: 1,
		RelayQoS:             "best-effort",
		FamilySeats:          0,
	},
	PlanPro: {
		ID:                   PlanPro,
		Name:                 "Pro",
		PricePerMonthCents:   500,
		BandwidthBytesPerMo:  100 * 1024 * 1024 * 1024,
		MaxServers:           3,
		MaxConcurrentStreams: 4,
		RelayQoS:             "priority",
		IncludesTranscoding:  true,
		FamilySeats:          0,
	},
	PlanFamily: {
		ID:                   PlanFamily,
		Name:                 "Family",
		PricePerMonthCents:   1000,
		BandwidthBytesPerMo:  500 * 1024 * 1024 * 1024,
		MaxServers:           5,
		MaxConcurrentStreams: 10,
		RelayQoS:             "priority",
		IncludesTranscoding:  true,
		FamilySeats:          6,
	},
}

// FreeOverageGrace is the soft buffer applied before we 402. Avoids
// jarring cutoffs at the exact byte boundary.
const FreeOverageGrace = 100 * 1024 * 1024 // 100 MiB

// MonthStart returns the first day of `t`'s month at 00:00 UTC, the
// boundary we use for bandwidth buckets.
func MonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
