package components

import (
	"slices"
	"strings"
)

// Retired names: what a release removed from the code while its migrations kept the rows.
//
// Maktaba replaces Plex: the Plex sync stages and the dizqueTV extension are gone, and slot
// 0215 drops none of their rows (a release's migrations lose no data, so a rollback runs
// the Down scripts instead of restoring the dump). Their job_stages, processing_jobs,
// worker_pools, extensions and component rows stay until a later cleanup migration deletes
// them; until then every reader leaves these names out: no queue, job, pool, failure or
// component line shows them, and nothing counts them as work waiting for a worker.
//
// pipeline/src/maktaba_pipeline/engine/retired.py holds the same names (a test there
// checks they agree).

// RetiredStages are the stages whose rows remain.
var RetiredStages = []string{
	"dizquetv_audit",
	"dizquetv_channels",
	"dizquetv_show",
	"plex_collection",
	"plex_collections_import",
	"plex_groups",
	"plex_index",
}

// RetiredPools are the worker pools only retired stages used.
var RetiredPools = []string{"audits", "network_tv"}

// RetiredExtensions are the extensions rows of removed extensions.
var RetiredExtensions = []string{"dizquetv"}

// RetiredComponents are the components rows (components, component_overrides,
// component_status, component_restarts) of removed components.
var RetiredComponents = []string{"ext.dizquetv"}

// RetiredStage reports a stage this release removed.
func RetiredStage(stage string) bool { return slices.Contains(RetiredStages, stage) }

// RetiredPool reports a pool only removed stages used.
func RetiredPool(pool string) bool { return slices.Contains(RetiredPools, pool) }

// RetiredExtension reports an extension this release removed.
func RetiredExtension(name string) bool { return slices.Contains(RetiredExtensions, name) }

// RetiredComponent reports a component this release removed.
func RetiredComponent(id string) bool { return slices.Contains(RetiredComponents, id) }

// NotRetiredStageSQL is a SQL condition true when column (a stage name) is not a retired
// stage, for the readers that filter in the query. The names are constants of this file,
// so the literal is safe to splice.
func NotRetiredStageSQL(column string) string {
	q := make([]string, len(RetiredStages))
	for i, s := range RetiredStages {
		q[i] = "'" + s + "'"
	}
	return column + " NOT IN (" + strings.Join(q, ", ") + ")"
}

// NotRetiredPoolSQL is NotRetiredStageSQL for a pool column.
func NotRetiredPoolSQL(column string) string {
	q := make([]string, len(RetiredPools))
	for i, s := range RetiredPools {
		q[i] = "'" + s + "'"
	}
	return column + " NOT IN (" + strings.Join(q, ", ") + ")"
}
