package main

// This machine's hardware tier (Epic 59 story 59.04). At each start of the whole machine,
// serve reads the hardware (hostinfo.Detect) and sets [server] tier from the memory limit,
// unless an admin picked one (tier_chosen = "admin"). A tier higher than the one saved,
// with none picked, moves the defaults up and says so (machine.tier.moved_up). The
// supervisor's loopback admin answers /config/machine with the reading, and takes an
// admin's pick (PUT): it applies at the next restart, as the other settings there do.
//
//	GET /config/machine   the machine, the tier and why, its defaults, the data disk
//	PUT /config/machine   {"tier": "light"|"standard"|"strong"|"auto"}: auto detects again

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

// TierChosenAdmin is [server] tier_chosen's value when an admin picked the tier.
const TierChosenAdmin = "admin"

// machineTier is this start's tier.
type machineTier struct {
	Machine  hostinfo.Machine
	Detected hostinfo.Tier
	Tier     hostinfo.Tier
	// Chosen: the admin picked Tier.
	Chosen bool
	// MovedUp: the detected tier is above the one the last start saved (memory was
	// added), and no tier was picked, so the defaults moved up.
	MovedUp bool
}

// runningTier is this serve's tier (nil before serve resolves it, and in a single role).
var runningTier atomic.Pointer[machineTier]

// detectMachine reads the hardware (tests replace it).
var detectMachine = hostinfo.Detect

// tierOf is the tier cfg's [server] says, with the machine m: the admin's pick when there
// is one, else the one detected; and whether the detected tier moved above the saved one.
func tierOf(s config.ServerSection, m hostinfo.Machine) machineTier {
	mt := machineTier{Machine: m, Detected: hostinfo.TierFor(m.Memory.LimitBytes, m.Arch)}
	saved, ok := hostinfo.ParseTier(s.Tier)
	if ok && strings.EqualFold(strings.TrimSpace(s.TierChosen), TierChosenAdmin) {
		mt.Tier, mt.Chosen = saved, true
		return mt
	}
	mt.Tier = mt.Detected
	mt.MovedUp = ok && mt.Detected.Rank() > saved.Rank()
	return mt
}

// resolveTier sets cfg's tier for this start and writes a detected one to the server.toml
// at path (when there is one), so the next start can tell a move. A file that cannot be
// written is said and changes nothing else.
func resolveTier(cfg *config.Config, path string, log io.Writer) machineTier {
	mt := tierOf(cfg.Server, detectMachine())
	if string(mt.Tier) != cfg.Server.Tier && !mt.Chosen {
		if file, found, err := config.LoadFrom(path); err == nil && found {
			file.Server.Tier, file.Server.TierChosen = string(mt.Tier), ""
			if err := file.SaveTo(path); err != nil {
				fmt.Fprintf(log, "[serve] level=warn the tier %s could not be written to %s: %v\n", mt.Tier, path, err)
			}
		}
	}
	cfg.Server.Tier = string(mt.Tier)
	if mt.Chosen {
		cfg.Server.TierChosen = TierChosenAdmin
	}
	how := "from the memory limit"
	if mt.Chosen {
		how = "picked by an admin"
	}
	fmt.Fprintf(log, "[serve] hardware tier %s (%s): %s; %d cores, platform %s\n", mt.Tier, how,
		hostinfo.ReasonFor(mt.Detected, mt.Machine), mt.Machine.CPU.Cores, mt.Machine.Platform)
	if mt.MovedUp {
		fmt.Fprintf(log, "[serve] this machine now has more memory: its defaults moved up to the %s tier\n", mt.Tier)
	}
	return mt
}

// machineDoc is GET /config/machine's answer.
type machineDoc struct {
	Machine  hostinfo.Machine  `json:"machine"`
	DataDisk hostinfo.DataDisk `json:"data_disk"`
	// Tier is the tier running now; Detected the one the memory gives; Chosen whether an
	// admin picked Tier; Saved the tier server.toml holds for the next start ("auto":
	// detected) and RestartNeeded whether it differs from what runs.
	Tier          hostinfo.Tier     `json:"tier"`
	Detected      hostinfo.Tier     `json:"detected"`
	Chosen        bool              `json:"chosen"`
	Saved         string            `json:"saved"`
	RestartNeeded bool              `json:"restart_needed"`
	CanRestart    bool              `json:"can_restart"`
	MovedUp       bool              `json:"moved_up"`
	Reason        hostinfo.Reason   `json:"reason"`
	Defaults      hostinfo.Defaults `json:"defaults"`
	// NAS: the platform is a NAS's own system.
	NAS bool `json:"nas"`
}

// machineDocOf is the document for what runs (mt) and what server.toml saves (file).
func machineDocOf(mt machineTier, file config.Config, canRestart bool) machineDoc {
	d := machineDoc{
		Machine:  mt.Machine,
		DataDisk: hostinfo.DataDiskOf(file.StateDir()),
		Tier:     mt.Tier, Detected: mt.Detected, Chosen: mt.Chosen, MovedUp: mt.MovedUp,
		Reason:     hostinfo.ReasonFor(mt.Tier, mt.Machine),
		Defaults:   hostinfo.DefaultsFor(mt.Tier, mt.Machine),
		NAS:        hostinfo.IsNAS(mt.Machine.Platform),
		CanRestart: canRestart,
	}
	next := tierOf(file.Server, mt.Machine)
	d.Saved = "auto"
	if next.Chosen {
		d.Saved = string(next.Tier)
	}
	d.RestartNeeded = next.Tier != mt.Tier || next.Chosen != mt.Chosen
	return d
}

// machineRoutes answers /config/machine (false: not its path).
func (c *configAdmin) machineRoutes(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/config/machine" {
		return false
	}
	mt := runningTier.Load()
	if mt == nil {
		m := detectMachine()
		cur, _, _ := config.LoadFrom(c.path)
		t := tierOf(cur.Server, m)
		mt = &t
	}
	canRestart := c.restart != nil || c.reload != nil
	switch r.Method {
	case http.MethodGet:
		file, _, err := config.LoadFrom(c.path)
		if err != nil {
			writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return true
		}
		writeConfigJSON(w, http.StatusOK, machineDocOf(*mt, file, canRestart))
	case http.MethodPut:
		var body struct {
			Tier string `json:"tier"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&body); err != nil {
			writeConfigJSON(w, http.StatusBadRequest, map[string]string{"error": "the body: " + err.Error()})
			return true
		}
		pick := strings.ToLower(strings.TrimSpace(body.Tier))
		t, ok := hostinfo.ParseTier(pick)
		if !ok && pick != "auto" {
			writeConfigJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"errors": map[string]string{"tier": "light, standard, strong or auto"}})
			return true
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		file, _, err := config.LoadFrom(c.path)
		if err != nil {
			writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return true
		}
		if ok {
			file.Server.Tier, file.Server.TierChosen = string(t), TierChosenAdmin
		} else {
			file.Server.Tier, file.Server.TierChosen = string(mt.Detected), ""
		}
		if err := file.SaveTo(c.path); err != nil {
			writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return true
		}
		fmt.Fprintf(os.Stderr, "[serve] hardware tier set from the web: %s (applies at the next start)\n", pick)
		writeConfigJSON(w, http.StatusOK, machineDocOf(*mt, file, canRestart))
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
	return true
}
