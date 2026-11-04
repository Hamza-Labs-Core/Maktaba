// Epic 51 story 51.12 wiring: a machine's components on the Machines routes (the
// components chosen as it is approved, the component switches kept in step with its stage
// switches), and the cluster channel's reload of a machine's stages after a component
// override rewrote them.
package router

import (
	"database/sql"
	"log/slog"
	"sync/atomic"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	clusterh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/cluster"
	componentsh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/components"
)

// machineStages is the cluster handler whose OnNodeChanged (the hub's, once it runs)
// tells a machine's processes to reload their stages.
var machineStages atomic.Pointer[clusterh.Handler]

// reloadMachineStages is every components service's OnMachineChanged.
func reloadMachineStages(node string) {
	if h := machineStages.Load(); h != nil && h.OnNodeChanged != nil {
		h.OnNodeChanged(node)
	}
}

// wireMachineComponents gives the cluster handler on db its components side.
func wireMachineComponents(h *clusterh.Handler, db *sql.DB, logger *slog.Logger) {
	s := components.NewService(db)
	s.Logger = logger
	s.OnMachineChanged = reloadMachineStages
	h.Components = componentsh.ClusterHook{H: &componentsh.Handler{Service: s, DB: db, Logger: logger}}
	machineStages.Store(h)
}
