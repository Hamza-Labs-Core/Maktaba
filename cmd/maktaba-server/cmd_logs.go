package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/logindex"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// newLogsCmd is `maktaba-server logs`: the log index role (Epic 53 story 53.09, catalog
// component logs.index), started by the supervisor like the other roles. It indexes the log
// keeper's segments into <data>/logs/index/logs.db and answers search and live tail on
// loopback for the API.
func newLogsCmd() *cobra.Command {
	var root, addr string
	cmd := &cobra.Command{
		Use:    "logs",
		Short:  "Run the log index (search and live tail of this machine's logs)",
		Hidden: true, // the supervisor starts it
		RunE: func(_ *cobra.Command, _ []string) error {
			// [logs] as saved: retention, the cap, the errors' retention (an earlier
			// version passed none, so the index kept its defaults whatever was set)
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			if root == "" {
				root = LogsRoot(cfg)
			}
			if addr == "" {
				addr = ports.LogsIndex.Addr()
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return runLogIndex(ctx, root, addr, cfg.Logs)
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "the log keeper's folder (default: <data>/logs)")
	cmd.Flags().StringVar(&addr, "addr", "", "the loopback address to answer on (default: the port registry's)")
	return cmd
}

// configureIndex gives the index [logs]' retention and cap and the metrics it scrapes.
func configureIndex(ix *logindex.Index, logs config.LogsSection) {
	r := logs.Resolved()
	ix.Retention, ix.ErrorRetention, ix.ReportRetention = r.Retention, r.ErrorRetention, r.ReportRetention
	// story 53.12: this machine's metrics (the API's admin port merges the supervisor's)
	if u := strings.TrimSpace(os.Getenv("MAKTABA_METRICS_URLS")); u != "" {
		ix.MetricsURLs = strings.Split(u, ",")
	} else {
		ix.MetricsURLs = []string{"http://" + ports.APIAdmin.Local() + "/metrics"}
	}
	ix.MaxBytes = r.MaxTotal * 35 / 100
}

func runLogIndex(ctx context.Context, root, addr string, logs config.LogsSection) error {
	ix, err := logindex.Open(root, time.Now)
	if err != nil {
		return fmt.Errorf("the log index: %w", err)
	}
	defer ix.Close()
	configureIndex(ix, logs)
	ix.Log = func(msg string, kv ...any) { logLine("info", msg, kv...) }
	if why := ix.Rebuilt(); why != "" {
		logLine("warn", "log_index_rebuilt", "why", why)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("the log index cannot listen on %s: %w", addr, err)
	}
	logLine("info", "log_index_started", "addr", addr)
	return ix.Serve(ctx, ln, 200*time.Millisecond)
}

// logLine writes one shared-schema line on stdout (the keeper's spool takes it).
func logLine(level, msg string, kv ...any) {
	m := map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano), "level": level, "msg": msg,
		"service": "logs", "component": "logs.index"}
	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok {
			m[k] = fmt.Sprint(kv[i+1])
		}
	}
	b, _ := json.Marshal(m)
	fmt.Println(string(b))
}
