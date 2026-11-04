package components

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Audit actions of the offline file and the import (story 51.16).
const (
	ActionOfflineMake     = "component.offline_make"
	ActionOfflineDownload = "component.offline_download"
	ActionImport          = "component.import"
	// ActionRestart is an admin's restart (story 51.08 §5): no desired row changes.
	ActionRestart = "component.restart"
)

// Restarted records an admin's restart of id: one audit row, component.restart, with
// 51.08 §5's payload (the desired row as before and after, the same; a batch id).
func (s *Service) Restarted(ctx context.Context, id string, actor Actor) error {
	var row any
	if d, err := s.Store.DesiredOf(ctx, id); err == nil {
		row = d
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return s.Record(ctx, actor, ActionRestart, id, map[string]any{"before": row, "after": row, "batch_id": uuid.NewString()})
}

// Component is the catalog's id (false: the catalog does not have it).
func (s *Service) Component(id string) (*components.Component, bool) {
	return s.cat().Get(id)
}

// Imported records what an offline file brought onto this machine: each id gets an
// audit row (component.import), and what the home wants absent becomes installed (off,
// files kept; the admin turns it on). Ids the catalog does not have are left out. The
// answer is what changed.
func (s *Service) Imported(ctx context.Context, ids []string, file string, actor Actor) (Result, error) {
	rows, _, err := s.Store.Desired(ctx, store.Home)
	if err != nil {
		return Result{}, err
	}
	var req Request
	var known []string
	for _, id := range ids {
		if !s.Known(id) {
			continue
		}
		known = append(known, id)
		if d, ok := rows[id]; ok && d.Want == components.WantAbsent {
			gen := d.Generation
			req.Changes = append(req.Changes, Change{ID: id, Want: components.WantInstalled, Generation: &gen})
		}
	}
	res := Result{Changed: []store.Desired{}}
	if len(req.Changes) > 0 {
		if res, err = s.Apply(ctx, req, actor); err != nil {
			return res, err
		}
	}
	for _, id := range known {
		if err := s.Record(ctx, actor, ActionImport, id, map[string]any{"file": file, "items": ids}); err != nil {
			return res, err
		}
	}
	return res, nil
}

// Record writes one audit row of the components (category components) that is not a
// desired-state change: an offline file made or fetched, an import. target is a
// component id, or "" for none.
func (s *Service) Record(ctx context.Context, actor Actor, action, target string, payload map[string]any) error {
	p := map[string]any{"by": actor.By}
	for k, v := range payload {
		p[k] = v
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	var user, tgt, kind any
	if looksLikeUUID(actor.UserID) {
		user = actor.UserID
	}
	if target != "" {
		tgt, kind = target, "component"
	}
	source := actor.Source
	if source == "" {
		source = "admin"
	}
	_, err = s.Store.DB.ExecContext(ctx, `INSERT INTO audit_log (category, action, actor_user_id, target_id,
		target_kind, actor_source, payload) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		store.AuditCategory, action, user, tgt, kind, source, string(b))
	return err
}

// looksLikeUUID: audit_log.actor_user_id is a UUID on Postgres; anything else is left out.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
	}
	return true
}
