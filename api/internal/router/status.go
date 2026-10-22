package router

import (
	"database/sql"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/systemstatus"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tv"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tvcluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// StatusDeps wires the System health page's status (GET /api/admin/system/status).
type StatusDeps struct {
	DB        *sql.DB
	Version   string
	StartTime time.Time
	// PipelineAddr is the pipeline's gRPC address as main.go resolved it
	// (MAKTABA_PIPELINE_ADDR, else MAKTABA_PIPELINE_GRPC_ADDR).
	PipelineAddr  string
	SupervisorURL string
	Secrets       secrets.Store
}

// MountStatus wires GET /api/admin/system/status (admin only). The streaming
// addresses come from the same variables the Live TV pages use.
func MountStatus(r chi.Router, d StatusDeps) *systemstatus.Handler {
	cluster := tvcluster.New(d.DB)
	h := &systemstatus.Handler{
		DB:            d.DB,
		Version:       d.Version,
		StartTime:     d.StartTime,
		StreamingURL:  cluster.StreamingURL,
		StreamingAddr: strings.TrimSpace(os.Getenv("MAKTABA_STREAMING_ADDR")),
		PipelineAddr:  d.PipelineAddr,
		SupervisorURL: d.SupervisorURL,
		Queues:        &queues.Handler{DB: d.DB},
		TVCluster:     cluster,
		TV:            &tv.Handler{DB: d.DB, Secrets: d.Secrets},
	}
	h.Mount(r)
	return h
}
