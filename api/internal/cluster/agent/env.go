package agent

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FromEnv reads the hub's settings (design-agent.md §4, §6, §7):
//
//	MAKTABA_CLUSTER_DIR             local cluster folder (overrides the share)
//	MAKTABA_SHARE_ROOT              share root, database form (default: from the library roots)
//	MAKTABA_PATH_MAP                database → local prefixes, the workers' --path-map format
//	MAKTABA_CLUSTER_RETENTION_DAYS  default 14
//	MAKTABA_CLUSTER_FILE_MB         default 256
//	MAKTABA_CLUSTER_LOG_LEVEL       default info
//	MAKTABA_LLM_QUEUE_TIMEOUT_S     default 300
//	MAKTABA_LLM_MAX_PARALLEL        default 1 (per runtime that does not say)
//	MAKTABA_CLUSTER_AGENT_RATE_PER_MIN  connection attempts per IP, default 60
//	MAKTABA_CORS_ALLOWED_ORIGINS    extra browser origins for /api/cluster/ws
//
// Bad values fall back to the default and are reported as warnings.
func FromEnv(getenv func(string) string) (Config, []string) {
	var warn []string
	intVar := func(name string, def int) int {
		v := strings.TrimSpace(getenv(name))
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			warn = append(warn, fmt.Sprintf("%s=%q ignored (want a whole number)", name, v))
			return def
		}
		return n
	}
	cfg := Config{
		LogLevel:        DefaultLogLevel,
		MetricsEvery:    DefaultMetricsEvery,
		RetentionDays:   intVar("MAKTABA_CLUSTER_RETENTION_DAYS", DefaultRetentionDays),
		FileMaxBytes:    int64(intVar("MAKTABA_CLUSTER_FILE_MB", DefaultFileMB)) << 20,
		LLMQueueTimeout: time.Duration(intVar("MAKTABA_LLM_QUEUE_TIMEOUT_S", int(DefaultQueueTimeout/time.Second))) * time.Second,
		LLMMaxParallel:  intVar("MAKTABA_LLM_MAX_PARALLEL", DefaultMaxParallel),
		AgentRatePerMin: intVar("MAKTABA_CLUSTER_AGENT_RATE_PER_MIN", DefaultAgentRatePerMin),
	}
	if v := strings.TrimSpace(getenv("MAKTABA_CLUSTER_LOG_LEVEL")); v != "" {
		if lv := normLevel(v); lv != "" {
			cfg.LogLevel = lv
		} else {
			warn = append(warn, fmt.Sprintf("MAKTABA_CLUSTER_LOG_LEVEL=%q ignored (debug, info, warning or error)", v))
		}
	}
	if cfg.RetentionDays == 0 {
		cfg.RetentionDays = DefaultRetentionDays
	}
	if cfg.AgentRatePerMin == 0 {
		cfg.AgentRatePerMin = -1 // 0 in the environment: no limit
	}
	pm, err := ParsePathMap(getenv("MAKTABA_PATH_MAP"))
	if err != nil {
		warn = append(warn, "MAKTABA_PATH_MAP ignored: "+err.Error())
	}
	cfg.Locator = &Locator{
		Override:  strings.TrimSpace(getenv("MAKTABA_CLUSTER_DIR")),
		ShareRoot: strings.TrimSpace(getenv("MAKTABA_SHARE_ROOT")),
		PathMap:   pm,
	}
	for _, o := range strings.Split(getenv("MAKTABA_CORS_ALLOWED_ORIGINS"), ",") {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		// coder/websocket matches origin hosts, not URLs
		if i := strings.Index(o, "://"); i >= 0 {
			o = o[i+3:]
		}
		cfg.BrowserOrigins = append(cfg.BrowserOrigins, strings.TrimSuffix(o, "/"))
	}
	return cfg, warn
}
