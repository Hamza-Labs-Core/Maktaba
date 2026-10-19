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
//	MAKTABA_CLUSTER_AGENT_RATE_PER_MIN  handshakes per IP a minute, default 30
//	MAKTABA_CLUSTER_IP              the home's IP folder name (default: its first private IPv4)
//	MAKTABA_CLUSTER_CACHE_HOURS     the database cache's window, default 24
//	MAKTABA_CORS_ALLOWED_ORIGINS    extra browser origins for /api/cluster/ws
//
//	MAKTABA_ARCHIVE_DIR             the home's long-term archive, default ~/.maktaba/archive
//	MAKTABA_ARCHIVE_KINDS           the kinds archived, default logs,jobs,llm,events
//	MAKTABA_ARCHIVE_DAYS            days kept in the archive, default 365 (0: no age limit)
//	MAKTABA_ARCHIVE_MAX_GB          the archive's size cap, default 20 (0: no size limit)
//	MAKTABA_HOME_AGENT_KEY_MODE     the home agent key file's mode: 0600 (default) or 0640
//	                                (0640 lets home workers of the API's group read it, e.g. Docker)
//
// The key files are read by the caller: MAKTABA_HOME_AGENT_KEY
// (MAKTABA_CLUSTER_KEY_PATH also accepted) and MAKTABA_HOME_CLUSTER_KEY
// (always 0600).
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
	cfg.CacheWindow = time.Duration(intVar("MAKTABA_CLUSTER_CACHE_HOURS", DefaultCacheHours)) * time.Hour
	cfg.Archive = &ArchiveConfig{
		Dir:      strings.TrimSpace(getenv("MAKTABA_ARCHIVE_DIR")),
		Kinds:    DefaultArchiveKinds,
		Days:     intVar("MAKTABA_ARCHIVE_DAYS", DefaultArchiveDays),
		MaxBytes: int64(intVar("MAKTABA_ARCHIVE_MAX_GB", DefaultArchiveMaxGB)) << 30,
	}
	if cfg.Archive.Dir == "" {
		cfg.Archive.Dir = DefaultArchiveDir()
	}
	if v := strings.TrimSpace(getenv("MAKTABA_ARCHIVE_KINDS")); v != "" {
		known := map[string]bool{}
		for _, k := range DayKinds {
			known[k] = true
		}
		var kinds []string
		for _, k := range strings.Split(v, ",") {
			k = strings.TrimSpace(k)
			switch {
			case k == "":
			case known[k]:
				kinds = append(kinds, k)
			default:
				warn = append(warn, fmt.Sprintf("MAKTABA_ARCHIVE_KINDS: %q is not a kind (%s)", k, strings.Join(DayKinds, ", ")))
			}
		}
		if len(kinds) > 0 {
			cfg.Archive.Kinds = kinds
		}
	}
	cfg.HomeKeyMode = KeyMode
	if v := strings.TrimSpace(getenv("MAKTABA_HOME_AGENT_KEY_MODE")); v != "" {
		if m, ok := ParseKeyMode(v); ok {
			cfg.HomeKeyMode = m
		} else {
			warn = append(warn, fmt.Sprintf("MAKTABA_HOME_AGENT_KEY_MODE=%q ignored (0600 or 0640)", v))
		}
	}
	if v := strings.TrimSpace(getenv("MAKTABA_CLUSTER_IP")); v != "" {
		if ValidIPDir(IPDir(v)) {
			cfg.HomeAddr = v
		} else {
			warn = append(warn, fmt.Sprintf("MAKTABA_CLUSTER_IP=%q ignored (an address or a host name)", v))
		}
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
