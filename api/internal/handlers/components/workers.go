package components

import (
	"context"
	"slices"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// pipelineRole is the supervisor's role of the pipeline's worker processes (the catalog
// writes them "pipeline/<instance>").
const pipelineRole = "pipeline"

// WorkerComponents are the catalog components with a pipeline worker process: the core
// pipeline, transcription's workers and the index worker.
func WorkerComponents(cat *components.Catalog) []string {
	var ids []string
	for _, c := range cat.Components() {
		for _, p := range c.Processes {
			if role, _, _ := strings.Cut(p, "/"); role == pipelineRole {
				ids = append(ids, c.ID)
				break
			}
		}
	}
	return ids
}

// RestartWorkers restarts this machine's pipeline workers (the Queues page's notice that
// workers do not run their jobs): the supervisor's restart of each worker component in
// ids, or of every one when ids is empty. The core pipeline is among them, which an admin
// cannot restart as a component (it is not switchable). An id that is not a worker
// component is left out. It answers how many processes restarted (0: none runs now); the
// first refusal or failed call ends it.
func (p *Packs) RestartWorkers(ctx context.Context, ids []string) (int, error) {
	n := 0
	for _, id := range WorkerComponents(components.Default()) {
		if len(ids) > 0 && !slices.Contains(ids, id) {
			continue
		}
		m, err := p.Restart(ctx, id)
		if err != nil {
			return n, err
		}
		n += m
	}
	return n, nil
}
