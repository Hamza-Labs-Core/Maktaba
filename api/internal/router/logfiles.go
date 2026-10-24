package router

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/logs"
)

// logFilesWait is how long a machine has to answer logs.files.
const logFilesWait = 30 * time.Second

// LogFiles is the logs page's FileSource: this machine's server.log, app.log and
// launcher.log from its supervisor's /logs/files (supervisorURL with the per-boot token
// in tokenFile), and a machine's over the machine channel (logs.files, its own agent).
func LogFiles(supervisorURL, tokenFile string, hub *HubRef, client *http.Client) logs.FileSource {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return func(ctx context.Context, node string, q url.Values) (json.RawMessage, error) {
		if node != "" {
			return nodeLogFiles(ctx, hub, node, q)
		}
		base := strings.TrimRight(strings.TrimSpace(supervisorURL), "/")
		if base == "" {
			return nil, logs.ErrNoSupervisor
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/logs/files?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		if b, err := os.ReadFile(tokenFile); err == nil && tokenFile != "" {
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(b)))
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode == http.StatusNotFound {
			return nil, logs.ErrNoSupervisor // a supervisor from before /logs/files
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		return raw, nil
	}
}

// nodeLogFiles asks a machine's own agent for its files.
func nodeLogFiles(ctx context.Context, ref *HubRef, node string, q url.Values) (json.RawMessage, error) {
	hub := ref.Get()
	if hub == nil {
		return nil, logs.ErrNodeOffline
	}
	sid := hub.MachineAgent(node)
	if sid == "" {
		return nil, logs.ErrNodeOffline
	}
	args, _ := json.Marshal(map[string]string{"files": q.Get("files"), "since": q.Get("since"),
		"until": q.Get("until"), "limit": q.Get("limit")})
	res, err := hub.CommandWithin(ctx, sid, "logs.files", args, "admin:logs", logFilesWait)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", logs.ErrNodeNoAnswer, err)
	}
	if !res.OK {
		var why string
		_ = json.Unmarshal(res.Detail, &why)
		if why == "" {
			why = string(res.Detail)
		}
		return nil, fmt.Errorf("%w: %s", logs.ErrNodeNoAnswer, why)
	}
	return res.Detail, nil
}
