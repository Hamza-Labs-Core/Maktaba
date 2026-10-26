package switchover

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// MovedHandler is GET /api/cluster/moved/{node} (public, on every machine): the latest switch's
// notice of the new home for that machine, sealed to it with the cluster key (only it
// opens it; only the home could have made it). A machine that was off during a switch
// asks the machine it knows as its home, which answers as a machine now. db is any copy
// of the home's database: the notices are in the switch's row. 404 when there is none.
func MovedHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		node := chi.URLParam(r, "node")
		if _, err := uuid.Parse(node); err != nil || db == nil {
			httperror.Write(w, r, httperror.NotFound("no notice for that machine"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var id, notice string
		var term int64
		err := db.QueryRowContext(ctx, `SELECT id::text, term, notices->>$1 FROM cluster_switchovers
		    WHERE notices ? $1 ORDER BY term DESC LIMIT 1`, node).Scan(&id, &term, &notice)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			httperror.Write(w, r, httperror.NotFound("no notice for that machine"))
			return
		case err != nil:
			httperror.Write(w, r, httperror.Internal("the notices").Wrap(err))
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"id": id, "term": term, "notice": notice})
	}
}

// TermOf is the cluster's current term in db (0 when it has none yet).
func TermOf(ctx context.Context, db *sql.DB) int64 {
	var t sql.NullInt64
	_ = db.QueryRowContext(ctx, `SELECT max(term) FROM cluster_terms`).Scan(&t)
	return t.Int64
}
