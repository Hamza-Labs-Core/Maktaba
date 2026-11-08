// Command plugin-api-dev serves the plugin API's trusted routes and the token mint on a
// test database, for the parity proof (Epic 54, story 54.26) and the first-party plugins'
// contract tests: the same statements executor, roles and tokens the API runs, with a
// first-party bundle read from a file (a developer's build of the plugins).
//
//	plugin-api-dev -dsn postgres://… -socket /tmp/x/plugin-api.sock -mint 127.0.0.1:0 -bundle firstparty.json
//
// It prints "ready <mint address>" on stdout once both listen. Never part of a release.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"

	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/relay"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/firstparty"
)

func main() {
	dsn := flag.String("dsn", "", "the test database")
	socket := flag.String("socket", "", "the plugin API's unix socket")
	mint := flag.String("mint", "127.0.0.1:0", "where the mint route listens (TCP)")
	bundle := flag.String("bundle", "", "a first-party bundle (tools/plugins/bundle.py's output)")
	plexURL := flag.String("plex-url", "", "trusted/service: the Plex the home's door forwards to (a fake)")
	plexToken := flag.String("plex-token", "", "trusted/service: the token the door adds")
	dizqueURL := flag.String("dizquetv-url", "", "trusted/service: the dizqueTV the home's door reaches (a fake; issue #827)")
	artBase := flag.String("art-base", "", "trusted/service: the logo host the dizqueTV plugin may check (dz_art_url)")
	flag.Parse()
	if *dsn == "" || *socket == "" {
		log.Fatal("plugin-api-dev: -dsn and -socket are needed")
	}
	if *bundle != "" {
		raw, err := os.ReadFile(*bundle)
		if err != nil {
			log.Fatal(err)
		}
		firstparty.UseBundle(raw)
		if _, err := firstparty.Plugins(); err != nil {
			log.Fatal(err)
		}
	}
	db, err := sql.Open("postgres", *dsn)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	tokens := plugins.NewTokens(db)
	s := &plugins.Server{DB: db, Tokens: tokens}
	if *plexURL != "" || *dizqueURL != "" {
		// story 54.35: the home's service door, to a fake Plex; issue #827: and a fake dizqueTV
		s.Service = relay.New(relay.Config{
			Settings: func(context.Context) (relay.Settings, error) { return relay.DefaultSettings(), nil },
			Upstream: func(_ context.Context, ext *relay.Extension) (relay.Upstream, error) {
				if ext.Name == relay.DizqueTV.Name {
					return relay.Upstream{Enabled: *dizqueURL != "", URL: *dizqueURL}, nil
				}
				return relay.Upstream{Enabled: *plexURL != "", URL: *plexURL, Secret: *plexToken}, nil
			},
			ArtBase: func(context.Context) (string, error) { return *artBase, nil },
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
	}
	go func() {
		if err := plugins.ListenUnix(ctx, *socket, s); err != nil {
			log.Fatal(err)
		}
	}()
	l, err := net.Listen("tcp", *mint)
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/plugins/invocations", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			JobID    int64  `json:"job_id"`
			PluginID string `json:"plugin_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		var m *plugins.Minted
		if in.JobID > 0 {
			m, err = tokens.Mint(r.Context(), in.JobID)
		} else {
			m, err = tokens.MintFingerprints(r.Context(), in.PluginID)
		}
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(m)
	})
	mux.HandleFunc("DELETE /internal/plugins/invocations/", func(w http.ResponseWriter, r *http.Request) {
		tokens.End(r.Context(), strings.TrimPrefix(r.URL.Path, "/internal/plugins/invocations/"))
		_ = json.NewEncoder(w).Encode(map[string]any{"ended": true})
	})
	srv := &http.Server{Handler: mux}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	fmt.Fprintf(os.Stdout, "ready %s\n", l.Addr())
	_ = srv.Serve(l)
}
