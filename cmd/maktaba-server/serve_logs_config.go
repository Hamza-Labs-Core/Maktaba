package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/logkeeper"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// The admin's Retention page and "Detailed logs for one hour" (Epic 53 story 53.11), on the
// supervisor's loopback admin (the API proxies them to admins):
//
//	GET  /config/logs        how long each kind is kept, the cap, what each uses now, and
//	                         which services log in detail until when
//	PUT  /config/logs        {logs_days, errors_days, reports_days, total_bytes}: saved in
//	                         [logs] of server.toml; the keeper takes it at once and the log
//	                         index restarts to take it
//	POST /config/logs/level  {service, level: debug|info, minutes}: a service's level file

// Ranges are the [logs] table's (config.LogsSection.Resolved).
var logsRanges = map[string][2]int64{
	"logs_days":    {1, 90},
	"errors_days":  {1, 365},
	"reports_days": {1, 365},
	"total_bytes":  {256 << 20, 20 << 30},
}

type logsUsage struct {
	Logs    int64 `json:"logs"`
	Index   int64 `json:"index"`
	Reports int64 `json:"reports"`
}

type logsDoc struct {
	LogsDays    int                 `json:"logs_days"`
	ErrorsDays  int                 `json:"errors_days"`
	ReportsDays int                 `json:"reports_days"`
	TotalBytes  int64               `json:"total_bytes"`
	Used        logsUsage           `json:"used"`
	Ranges      map[string][2]int64 `json:"ranges"`
	// Detailed: each service raised to debug, until when
	Detailed map[string]string `json:"detailed"`
}

type logsChange struct {
	LogsDays    *int64 `json:"logs_days"`
	ErrorsDays  *int64 `json:"errors_days"`
	ReportsDays *int64 `json:"reports_days"`
	TotalBytes  *int64 `json:"total_bytes"`
}

// logsAdmin is what the routes act on (nil fields: that part is not there).
type logsAdmin struct {
	keeper  *logkeeper.Keeper
	restart func(id string) int
	now     func() time.Time
}

func (c *configAdmin) logsDoc() (logsDoc, error) {
	cfg, _, err := config.LoadFrom(c.path)
	if err != nil {
		return logsDoc{}, err
	}
	r := cfg.Logs.Resolved()
	root := LogsRoot(cfg)
	now := time.Now
	if c.logs != nil && c.logs.now != nil {
		now = c.logs.now
	}
	d := logsDoc{LogsDays: int(r.Retention / (24 * time.Hour)), ErrorsDays: int(r.ErrorRetention / (24 * time.Hour)),
		ReportsDays: int(r.ReportRetention / (24 * time.Hour)), TotalBytes: r.MaxTotal, Ranges: logsRanges,
		Used: logsUsage{Logs: dirSize(filepath.Join(root, "segments")), Index: dirSize(filepath.Join(root, "index")),
			Reports: dirSize(filepath.Join(root, "reports"))},
		Detailed: map[string]string{}}
	for _, role := range supervisor.LevelRoles {
		if u := supervisor.LevelUntil(cfg, role, now()); !u.IsZero() {
			d.Detailed[string(role)] = u.Format(time.RFC3339)
		}
	}
	return d, nil
}

// applyLogs checks each value against its range and saves the change; the field errors
// name what is out of range.
func applyLogs(path string, ch logsChange) (config.ResolvedLogs, []config.FieldError, error) {
	var bad []config.FieldError
	check := func(name string, v *int64) {
		if v == nil {
			return
		}
		rg := logsRanges[name]
		if *v < rg[0] || *v > rg[1] {
			bad = append(bad, config.FieldError{Field: "logs." + name,
				Message: fmt.Sprintf("must be between %d and %d", rg[0], rg[1])})
		}
	}
	check("logs_days", ch.LogsDays)
	check("errors_days", ch.ErrorsDays)
	check("reports_days", ch.ReportsDays)
	check("total_bytes", ch.TotalBytes)
	if len(bad) > 0 {
		return config.ResolvedLogs{}, bad, nil
	}
	cfg, _, err := config.LoadFrom(path)
	if err != nil {
		return config.ResolvedLogs{}, nil, err
	}
	days := func(n int64) string { return strconv.FormatInt(n, 10) + "d" }
	if ch.LogsDays != nil {
		cfg.Logs.Retention = days(*ch.LogsDays)
	}
	if ch.ErrorsDays != nil {
		cfg.Logs.ErrorRetention = days(*ch.ErrorsDays)
	}
	if ch.ReportsDays != nil {
		cfg.Logs.ReportRetention = days(*ch.ReportsDays)
	}
	if ch.TotalBytes != nil {
		cfg.Logs.MaxTotalSize = strconv.FormatInt(*ch.TotalBytes>>20, 10) + "MB"
		cfg.Logs.MaxSegmentsSize = "" // the segments' share follows the total
	}
	if err := cfg.SaveTo(path); err != nil {
		return config.ResolvedLogs{}, nil, err
	}
	return cfg.Logs.Resolved(), nil, nil
}

// logsRoutes serves the three routes (false: not one of them).
func (c *configAdmin) logsRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/config/logs" && r.Method == http.MethodGet:
		c.logsGet(w)
	case r.URL.Path == "/config/logs" && r.Method == http.MethodPut:
		var ch logsChange
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&ch); err != nil {
			writeConfigJSON(w, http.StatusBadRequest, map[string]string{"error": "the settings: " + err.Error()})
			return true
		}
		c.mu.Lock()
		res, bad, err := applyLogs(c.path, ch)
		c.mu.Unlock()
		switch {
		case len(bad) > 0:
			writeConfigJSON(w, http.StatusBadRequest, map[string]any{"errors": bad})
			return true
		case err != nil:
			writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return true
		}
		if c.logs != nil {
			if c.logs.keeper != nil {
				c.logs.keeper.SetLimits(res.Retention, res.DebugRetention, res.MaxSegments)
			}
			if c.logs.restart != nil {
				c.logs.restart("logs.index") // it reads [logs] at its start
			}
		}
		fmt.Fprintf(os.Stderr, "[serve] the diagnostics' retention changed from the web\n")
		c.logsGet(w)
	case r.URL.Path == "/config/logs/level" && r.Method == http.MethodPost:
		var body struct {
			Service string `json:"service"`
			Level   string `json:"level"`
			Minutes int    `json:"minutes"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
			writeConfigJSON(w, http.StatusBadRequest, map[string]string{"error": "the body: " + err.Error()})
			return true
		}
		cfg, _, err := config.LoadFrom(c.path)
		if err != nil {
			writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return true
		}
		now := time.Now
		if c.logs != nil && c.logs.now != nil {
			now = c.logs.now
		}
		_, err = supervisor.SetLevelFor(cfg, supervisor.Role(body.Service), body.Level,
			time.Duration(body.Minutes)*time.Minute, now())
		switch {
		case errors.Is(err, supervisor.ErrLevelRole):
			writeConfigJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return true
		case err != nil:
			writeConfigJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return true
		}
		c.logsGet(w)
	case r.URL.Path == "/config/logs" || r.URL.Path == "/config/logs/level":
		_, _ = io.Copy(io.Discard, r.Body)
		http.Error(w, "method", http.StatusMethodNotAllowed)
	default:
		return false
	}
	return true
}

func (c *configAdmin) logsGet(w http.ResponseWriter) {
	d, err := c.logsDoc()
	if err != nil {
		writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeConfigJSON(w, http.StatusOK, d)
}
