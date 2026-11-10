package logs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// machineInfo is the supervisor's /config/machine (Epic 59 story 59.04): the hardware, the
// tier and why, for machine.json in an admin's bundle. Without a supervisor, or when it
// does not answer, the file says why instead.
func (h *Handler) machineInfo(ctx context.Context) []byte {
	sup := h.supervisor()
	if sup == nil {
		return mustJSON(map[string]string{"status": "unknown", "reason": "no supervisor"})
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := sup.Do(ctx, http.MethodGet, "/config/machine", nil, "")
	if err != nil {
		return mustJSON(map[string]string{"status": "unknown", "reason": "the supervisor did not answer"})
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode != http.StatusOK || !json.Valid(b) {
		return mustJSON(map[string]any{"status": "unknown", "reason": "the supervisor answered " + resp.Status})
	}
	return b
}
