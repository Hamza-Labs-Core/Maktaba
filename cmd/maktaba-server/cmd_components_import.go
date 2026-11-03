package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// Epic 51 story 51.16: `maktaba-server components import <file>` (the command itself is
// 51.08's `components` group) hands an offline file on this machine to the running
// supervisor, which verifies and installs it (POST /components/import {"path"}).

// importOffline asks the running supervisor of cfg to import the offline file at p.
func importOffline(ctx context.Context, cfg config.Config, p string, out io.Writer) error {
	base := supervisor.StateURL(cfg)
	tok, err := os.ReadFile(supervisor.LocalTokenPath(cfg))
	if base == "" || err != nil || strings.TrimSpace(string(tok)) == "" {
		return fmt.Errorf("no running maktaba-server answers on this machine: start it, then import the file")
	}
	return importOfflineAt(ctx, &http.Client{}, base, strings.TrimSpace(string(tok)), p, out)
}

// importOfflineAt is importOffline against the supervisor admin at base with token.
func importOfflineAt(ctx context.Context, c *http.Client, base, token, p string, out io.Writer) error {
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"path": abs})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/components/import", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var doc struct {
		Installed []string `json:"installed"`
		Error     string   `json:"error"`
		Detail    string   `json:"detail"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("the server answered %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode != http.StatusOK {
		if len(doc.Installed) > 0 {
			fmt.Fprintf(out, "installed before the failure: %s\n", strings.Join(doc.Installed, ", "))
		}
		return fmt.Errorf("%s: %s", doc.Error, doc.Detail)
	}
	if len(doc.Installed) == 0 {
		fmt.Fprintln(out, "nothing to install: everything the file holds is installed already")
		return nil
	}
	fmt.Fprintf(out, "installed: %s\n", strings.Join(doc.Installed, ", "))
	return nil
}
