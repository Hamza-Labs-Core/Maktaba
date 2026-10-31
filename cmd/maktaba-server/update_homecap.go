package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// A machine joined to a home never goes ahead of it (the owner's rule, 2026-10-28: that
// day windows-pc ran 0.3.42 while its home rolled back to 0.3.41 and put its database back
// to schema 167, and updates only go up). Its new releases come from the home's order,
// after the home committed them; `maktaba-server update` on such a machine installs none
// newer than the release its home committed last (the home's /api/cluster/self
// `committed`). A machine joined to no home updates as it always did.

// homeCeilingOf reads this machine's ceiling: joined false when it follows no home.
var homeCeilingOf = func(ctx context.Context) (string, bool, error) {
	return readHomeCeiling(ctx, config.LocalNodeConfig(), &http.Client{Timeout: 5 * time.Second})
}

// readHomeCeiling asks the home in nc for the release it committed last.
func readHomeCeiling(ctx context.Context, nc config.NodeConfig, hc *http.Client) (string, bool, error) {
	if !nc.Joined() {
		return "", false, nil
	}
	base := nc.HomeURL()
	if base == "" {
		return "", true, fmt.Errorf("this machine's node config names no home address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/cluster/self", nil)
	if err != nil {
		return "", true, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", true, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", true, fmt.Errorf("the home answered %d", resp.StatusCode)
	}
	var self struct {
		Committed string `json:"committed"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&self); err != nil {
		return "", true, err
	}
	return strings.TrimSpace(self.Committed), true, nil
}

// checkHomeCeiling refuses tag on a joined machine when it is newer than the release its
// home committed, or when that release cannot be read (nothing is installed on a guess).
func checkHomeCeiling(tag, ceiling string, joined bool, readErr error) error {
	if !joined {
		return nil
	}
	if readErr != nil || ceiling == "" {
		why := "it does not say"
		if readErr != nil {
			why = readErr.Error()
		}
		return fmt.Errorf("this machine follows a home whose committed release could not be read (%s): "+
			"it updates when the home orders it, never ahead of the home", why)
	}
	if releaseverify.CompareVersions(tag, ceiling) > 0 {
		return fmt.Errorf("this machine follows a home that committed %s: %s is newer, and a joined machine "+
			"never goes ahead of its home (update the home first; this machine follows it by itself)", ceiling, tag)
	}
	return nil
}

// refuseAboveHome is checkHomeCeiling for this machine.
func refuseAboveHome(ctx context.Context, tag string) error {
	ceiling, joined, err := homeCeilingOf(ctx)
	return checkHomeCeiling(tag, ceiling, joined, err)
}
