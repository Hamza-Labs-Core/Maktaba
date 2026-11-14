package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
)

const relayUsage = `usage: maktaba-cloud relay <command> [id] [--config path]
  list           every relay of the pool: its state, heartbeat age, capacity, health
  drain    <id>  take the relay out of the pool: its homes move to their next relays
  activate <id>  put a drained (or down) relay back in the pool
  down     <id>  mark a relay out of service (a machine being replaced)`

// relayAdmin is the relays table as the relay command uses it.
type relayAdmin interface {
	Relays(ctx context.Context) ([]relaypkg.PoolRelay, error)
	SetState(ctx context.Context, id, state string) (bool, error)
}

// runRelayCmd manages the relay pool's members from a node's shell (story 50.16): a relay is
// drained before it is taken out (its homes rebalance away at 20 a second per relay) and
// activated when it is back.
func runRelayCmd(args []string) {
	cfgPath := ""
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			cfgPath = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	logger := newLogger("relay")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Error("config load", "err", err)
		os.Exit(2)
	}
	pool, err := db.Open(cfg.Database.URL, 2, 1, time.Minute)
	if err != nil {
		logger.Error("db open", "err", err)
		os.Exit(1)
	}
	defer func() { _ = pool.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := relayCommand(ctx, relaypkg.PGPoolStore{DB: pool.DB}, rest, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

// relayCommand runs one relay command against store, printing to out.
func relayCommand(ctx context.Context, store relayAdmin, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", relayUsage)
	}
	switch args[0] {
	case "list":
		rs, err := store.Relays(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSTATE\tHEARTBEAT\tCAPACITY\tPUBLIC\tPRIVATE\tHEALTHY")
		for _, r := range rs {
			fmt.Fprintf(tw, "%s\t%s\t%s ago\t%d Mbit/s\t%s\t%s\t%v\n", r.ID, r.State, r.Age.Round(time.Second), r.CapacityMbps, r.PublicIP, r.PrivateIP, r.Healthy())
		}
		return tw.Flush()
	case "drain", "activate", "down":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			return fmt.Errorf("%s", relayUsage)
		}
		state := map[string]string{"drain": relaypkg.RelayDraining, "activate": relaypkg.RelayActive, "down": relaypkg.RelayDown}[args[0]]
		ok, err := store.SetState(ctx, args[1], state)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("relay %q is not in the pool (maktaba-cloud relay list)", args[1])
		}
		fmt.Fprintf(out, "relay %s is %s; every relay sees it within 20 s\n", args[1], state)
		return nil
	default:
		return fmt.Errorf("%s", relayUsage)
	}
}
