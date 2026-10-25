package main

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor"
)

// linkClient is the front door's HTTP client: a machine's channel is read over that
// machine's cluster link when it has one (Epic 35 story 35.16), else from its streaming
// port while the home does not require the link.
func linkClient(pool *pgxpool.Pool) *http.Client {
	c := frontdoor.NewClient()
	mode := &cluster.LinkMode{Pool: pool}
	c.Transport = frontdoor.LinkPullFromEnv(c.Transport, mode.Required)
	return c
}
