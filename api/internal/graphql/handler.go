package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	gql "github.com/graph-gophers/graphql-go"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Handler is the GraphQL endpoint:
//
//	POST /graphql          queries and mutations (GraphQL over HTTP)
//	GET  /graphql          subscriptions (graphql-transport-ws upgrade)
//	GET  /graphql/schema   the SDL
//
// Every operation needs an authenticated caller; what the caller may do
// is then decided by the REST routes the fields run (bridge.go).
type Handler struct {
	Resolvers *Resolvers
	// MaxDepth / MaxComplexity bound an operation (cost.go); zero means
	// DefaultMaxDepth / DefaultMaxComplexity.
	MaxDepth      int
	MaxComplexity int
	// Persisted is the persisted-query store (persisted.go); nil holds
	// nothing, so every hash misses. PersistedOnly reports allowlist mode
	// (the graphql.persisted_only setting); nil is off.
	Persisted     *PersistedStore
	PersistedOnly func(context.Context) bool

	once   sync.Once
	parsed *gql.Schema
}

// schema parses the SDL against the resolvers once.
func (h *Handler) schema() *gql.Schema {
	h.once.Do(func() {
		h.parsed = gql.MustParseSchema(Schema, &root{rs: h.Resolvers},
			gql.UseFieldResolvers(),
			gql.UseStringDescriptions(),
			gql.MaxParallelism(8),
		)
	})
	return h.parsed
}

// gqlRequest is the standard GraphQL-over-HTTP POST body.
type gqlRequest struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName"`
	Variables     map[string]any `json:"variables"`
	// Persisted queries (persisted.go): Apollo's extension, or the id.
	Extensions       persistedExt `json:"extensions"`
	PersistedQueryID string       `json:"persistedQueryId"`
}

// maxBody caps a POST /graphql body.
const maxBody = 256 << 10

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/graphql/schema" {
		w.Header().Set("Content-Type", "application/graphql; charset=utf-8")
		_, _ = w.Write([]byte(Schema))
		return
	}
	if h.Resolvers == nil {
		writeGQL(w, http.StatusServiceUnavailable, map[string]any{"errors": []map[string]any{
			gqlErr("GraphQL needs the API database, which this build has not opened", "unavailable")}})
		return
	}
	if principal.FromContext(r.Context()) == nil {
		writeGQL(w, http.StatusUnauthorized, map[string]any{"errors": []map[string]any{
			gqlErr("authentication required", "unauthenticated")}})
		return
	}
	if r.Method == http.MethodGet {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			h.serveWS(w, r)
			return
		}
		writeGQL(w, http.StatusMethodNotAllowed, map[string]any{"errors": []map[string]any{
			gqlErr("send operations as POST /graphql; GET upgrades to graphql-transport-ws for subscriptions", "bad-request")}})
		return
	}

	var req gqlRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(&req); err != nil {
		writeGQL(w, http.StatusBadRequest, map[string]any{"errors": []map[string]any{
			gqlErr("malformed GraphQL request body: want {query, operationName, variables}", "bad-request")}})
		return
	}
	register, status, perrs := h.resolvePersisted(r.Context(), &req)
	if perrs != nil {
		writeGQL(w, status, map[string]any{"errors": perrs})
		return
	}
	if req.Variables == nil {
		req.Variables = map[string]any{}
	}

	plan, errs := h.analyse(req.Query, req.OperationName, req.Variables)
	if errs != nil {
		writeGQL(w, http.StatusBadRequest, map[string]any{"errors": errs})
		return
	}
	if register {
		h.registerPersisted(r.Context(), req.Query)
	}
	if plan.operation == "subscription" {
		writeGQL(w, http.StatusBadRequest, map[string]any{"errors": []map[string]any{
			gqlErr("subscriptions run over the graphql-transport-ws WebSocket on GET /graphql", "bad-request")}})
		return
	}

	ctx := withOuter(r.Context(), r)
	resp := h.schema().Exec(ctx, req.Query, req.OperationName, req.Variables)
	writeGQL(w, http.StatusOK, resp)
}

func writeGQL(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/graphql-response+json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// NewHandlerFromEnv builds the handler over rs with the limits from
// MAKTABA_GRAPHQL_MAX_DEPTH and MAKTABA_GRAPHQL_MAX_COMPLEXITY (unset or
// invalid → the defaults).
func NewHandlerFromEnv(rs *Resolvers) *Handler {
	return &Handler{
		Resolvers:     rs,
		MaxDepth:      envInt("MAKTABA_GRAPHQL_MAX_DEPTH"),
		MaxComplexity: envInt("MAKTABA_GRAPHQL_MAX_COMPLEXITY"),
	}
}

func envInt(name string) int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
