package broadcast

// The cloud's posts to the broadcast edge for viewer access (story 50.26): each is a control post
// signed with the broadcast key, sent to the ingest host.
//
//	PUT  /v1/control/access   a publisher's AccessState (typ "access"); the edge keeps the newest
//	POST /v1/control/passes   {typ "passes", pub}: the publisher's guest passes and invite uses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lib/pq"

	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
)

// EdgeClient sends one signed control post to the edge and answers its status and body.
type EdgeClient interface {
	Post(ctx context.Context, method, path, body string) (int, []byte, error)
}

// HTTPEdge is the EdgeClient of a real edge (broadcast.ingest_url).
type HTTPEdge struct {
	Base   string
	Client *http.Client
}

func (e HTTPEdge) Post(ctx context.Context, method, path, body string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(e.Base, "/")+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "text/plain")
	c := e.Client
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Second}
	}
	res, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	return res.StatusCode, b, err
}

// AccessState builds a publisher's access state from the database.
func (d *Deps) AccessState(ctx context.Context, server string) (bc.AccessState, error) {
	s := bc.AccessState{Pub: server, Chans: map[string]bc.AccessChannel{}, Codes: map[string]string{},
		RevokedInvites: []string{}, RevokedPasses: []string{}}
	if err := d.DB.QueryRowContext(ctx, `
        SELECT broadcast_access_version, broadcast_max_sessions_per_ip, broadcast_daily_minutes FROM servers WHERE id = $1`, server).
		Scan(&s.Version, &s.MaxSessionsPerIP, &s.DailyMinutes); err != nil {
		return s, err
	}
	chs, err := d.channels(ctx, d.DB, server)
	if err != nil {
		return s, err
	}
	p, err := d.Store.Publisher(ctx, server)
	if err != nil {
		return s, err
	}
	code, _, err := d.openRefusal(ctx, p)
	if err != nil {
		return s, err
	}
	s.OpenAllowed = code == ""
	for _, c := range chs {
		s.Chans[c.EdgeID] = bc.AccessChannel{Access: c.Access, MaxViewers: c.MaxViewers, Indexable: c.Indexable}
	}
	// revoked invites the edge must still refuse (an expired one refuses itself a day later);
	// live invites' short codes
	rows, err := d.DB.QueryContext(ctx, `
        SELECT id::text, short_code, token, revoked_at IS NOT NULL FROM broadcast_invites
         WHERE server_id = $1 AND expires_at > now() - interval '1 day'`, server)
	if err != nil {
		return s, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, code, token string
		var revoked bool
		if err := rows.Scan(&id, &code, &token, &revoked); err != nil {
			return s, err
		}
		if revoked {
			s.RevokedInvites = append(s.RevokedInvites, id)
		} else {
			s.Codes[code] = token
		}
	}
	if err := rows.Err(); err != nil {
		return s, err
	}
	var passes []string
	if err := d.DB.QueryRowContext(ctx, `SELECT COALESCE(array_agg(pass_id ORDER BY pass_id), '{}') FROM broadcast_revoked_passes WHERE server_id = $1`, server).
		Scan(pq.Array(&passes)); err != nil {
		return s, err
	}
	if passes != nil {
		s.RevokedPasses = passes
	}
	return s, nil
}

// pushOne pushes a publisher's access state after a change, with its own short deadline so a
// slow edge never holds the request; a failure is left for PushPending.
func (d *Deps) pushOne(ctx context.Context, server string) {
	if d.Edge == nil || d.Key == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := d.push(ctx, server); err != nil && d.Logger != nil {
		d.Logger.Warn("broadcast: access push failed; retried", "server_id", server, "err", err)
	}
}

func (d *Deps) push(ctx context.Context, server string) error {
	s, err := d.AccessState(ctx, server)
	if err != nil {
		return err
	}
	signed, err := bc.SignAccess(*d.Key, s, d.now())
	if err != nil {
		return err
	}
	status, body, err := d.Edge.Post(ctx, http.MethodPut, "/v1/control/access", signed)
	if err != nil {
		return err
	}
	// 409: the edge holds a newer version already (another node pushed it)
	if status/100 != 2 && status != http.StatusConflict {
		return fmt.Errorf("edge answered %d: %s", status, bytes.TrimSpace(body))
	}
	_, err = d.DB.ExecContext(ctx, `UPDATE servers SET broadcast_access_pushed = $2 WHERE id = $1 AND broadcast_access_pushed < $2`, server, s.Version)
	return err
}

// PushPending pushes every publisher whose access state the edge hasn't taken (the worker runs
// it every minute), in batches until none is left, so an hourly refresh of every publisher never
// holds a real change back past the minute (a change is pushed by its own request first). It
// answers how many were pushed; one that fails stays for the next run.
func (d *Deps) PushPending(ctx context.Context) (int, error) {
	if d.Edge == nil || d.Key == nil {
		return 0, nil
	}
	n, after := 0, ""
	for ctx.Err() == nil {
		rows, err := d.DB.QueryContext(ctx, `
            SELECT id::text FROM servers WHERE broadcast_access_version > broadcast_access_pushed AND id::text > $1
             ORDER BY id::text LIMIT 200`, after)
		if err != nil {
			return n, err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return n, err
			}
			ids = append(ids, id)
		}
		_ = rows.Close()
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if err := d.push(ctx, id); err != nil {
				if d.Logger != nil {
					d.Logger.Warn("broadcast: access push failed", "server_id", id, "err", err)
				}
				continue
			}
			n++
		}
		after = ids[len(ids)-1]
	}
	return n, nil
}

// RefreshAll marks every publisher with a live channel or a live invite changed, so the next
// PushPending re-sends its state. The worker runs it hourly: what the access state derives from
// outside its own tables (the plan, a confirmed email, the 7 days since the first publish,
// broadcast.open_access.enabled) reaches the edge within the hour, and the edge never keeps an
// open channel open past it.
func (d *Deps) RefreshAll(ctx context.Context) (int64, error) {
	res, err := d.DB.ExecContext(ctx, `
        UPDATE servers SET broadcast_access_version = broadcast_access_version + 1
         WHERE id IN (SELECT server_id FROM broadcast_channels WHERE unpublished_at IS NULL
                      UNION SELECT server_id FROM broadcast_invites WHERE expires_at > now() - interval '1 day')`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// EdgePass is one device's guest pass as the edge reports it: a random id, its invite, and when
// it was first and last used. No identity.
type EdgePass struct {
	ID       string `json:"id"`
	Invite   string `json:"inv"`
	FirstUse int64  `json:"first_use"`
	LastUse  int64  `json:"last_use"`
	Revoked  bool   `json:"revoked"`
}

type edgePasses struct {
	Passes []EdgePass     `json:"passes"`
	Uses   map[string]int `json:"uses"`
}

func (d *Deps) edgePasses(ctx context.Context, server string) (edgePasses, error) {
	var out edgePasses
	signed, err := bc.SignControl(*d.Key, "passes", map[string]any{"pub": server}, d.now(), time.Minute)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	status, body, err := d.Edge.Post(ctx, http.MethodPost, "/v1/control/passes", signed)
	if err != nil {
		return out, err
	}
	if status != http.StatusOK {
		return out, fmt.Errorf("edge answered %d", status)
	}
	err = json.Unmarshal(body, &out)
	return out, err
}

// edgeUses is each invite's uses at the edge (nil when the edge didn't answer).
func (d *Deps) edgeUses(ctx context.Context, server string) map[string]int {
	if d.Edge == nil || d.Key == nil {
		return nil
	}
	p, err := d.edgePasses(ctx, server)
	if err != nil {
		return nil
	}
	if p.Uses == nil {
		return map[string]int{}
	}
	return p.Uses
}
