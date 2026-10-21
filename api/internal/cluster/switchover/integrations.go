package switchover

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
)

// Integration states.
const (
	Moved    = "moved"
	Failed   = "failed"
	NeedsYou = "needs_you"
	NotUsed  = "not_used"
	// Pending: asked to move, its answer not in yet (the switch waits for it).
	Pending = "pending"
)

// Secrets is the home's sealed secrets as a switch checks them (secrets.Store).
type Secrets interface {
	Get(ctx context.Context, name string) ([]byte, error)
}

// HomeIntegrations checks, on the new home, the outside services the old home used:
// each moved with the kit and checked from here, or what only a person can do.
//
//   - secrets: every secret the old home kept opens with the key it handed over;
//   - plex, cloudflare_stream, simulcast, web_search: their secrets open (they are
//     bound to no machine);
//   - plex_live_tv: the plex_livetv stage moves Plex's tuner and guide to this machine's
//     address (pending until it answers);
//   - hugging_face: the token came with the kit;
//   - jwt: the home's JWT keys (an operator's MAKTABA_JWT_*_PEM) came with the kit and this
//     machine's API has them;
//   - youtube: a browser's YouTube cookies are that machine's: someone signs in on this
//     machine's browser (needs you);
//   - llm: a model runtime on the old home is not here unless this machine has one.
func HomeIntegrations(db *sql.DB, store Secrets, hfToken string, publicStream func() string) Integrations {
	return func(ctx context.Context) map[string]Integration {
		out := map[string]Integration{}
		names := map[string]bool{}
		rows, err := db.QueryContext(ctx, `SELECT name FROM home_secrets`)
		if err == nil {
			for rows.Next() {
				var n string
				if rows.Scan(&n) == nil {
					names[n] = true
				}
			}
			_ = rows.Close()
		}
		opened := func(prefix string) (has, ok bool, bad []string) {
			for n := range names {
				if !strings.HasPrefix(n, prefix) {
					continue
				}
				has = true
				if store == nil {
					bad = append(bad, n)
					continue
				}
				if _, err := store.Get(ctx, n); err != nil {
					bad = append(bad, n)
				}
			}
			return has, len(bad) == 0, bad
		}
		if has, ok, bad := opened(""); !has {
			out["secrets"] = Integration{State: NotUsed}
		} else if ok {
			out["secrets"] = Integration{State: Moved, Detail: fmt.Sprintf("%d", len(names))}
		} else {
			out["secrets"] = Integration{State: Failed, Detail: strings.Join(bad, ", ")}
		}
		for name, prefix := range map[string]string{"plex": "ext.plex.token", "cloudflare_stream": "cf_api_token",
			"simulcast": "simulcast_", "web_search": "ext.web_search."} {
			switch has, ok, _ := opened(prefix); {
			case !has:
				out[name] = Integration{State: NotUsed}
			case ok:
				out[name] = Integration{State: Moved}
			default:
				out[name] = Integration{State: Failed, Detail: "its secret does not open with the key handed over"}
			}
		}
		out["plex_live_tv"] = liveTV(ctx, db, publicStream)
		if hfToken != "" {
			if _, err := os.Stat(hfToken); err == nil {
				out["hugging_face"] = Integration{State: Moved}
			} else {
				out["hugging_face"] = Integration{State: NotUsed}
			}
		}
		if os.Getenv("MAKTABA_JWT_PRIVATE_KEY_PEM") != "" {
			out["jwt"] = Integration{State: Moved}
		} else {
			out["jwt"] = Integration{State: NotUsed}
		}
		out["youtube"] = youtube(ctx, db)
		out["llm"] = llm(ctx, db)
		return out
	}
}

// plexLiveStage is the pipeline stage that keeps Maktaba in Plex's Live TV, and the
// subject it runs on (the catalog).
const (
	plexLiveStage   = "plex_livetv"
	catalogSubject  = "00000000-0000-0000-0000-00000000ca7a"
	plexLiveRequest = "switchover"
)

// liveTV moves Plex's Live TV to this home when the old home had Maktaba in it: the
// plex_livetv stage is asked once to register the tuner and guide at this machine's
// address (it moves the tuner Plex has at the old home's, Plex's DVR and channels kept)
// and its answer is reported. A tuner address set by hand in Settings
// (rules.plexlive.tuner_url) naming another machine stays as it is: a person changes it.
func liveTV(ctx context.Context, db *sql.DB, publicStream func() string) Integration {
	var (
		registered bool
		status     []byte
		requested  sql.NullString
		asked      sql.NullTime
		lastErr    sql.NullString
	)
	err := db.QueryRowContext(ctx, `SELECT COALESCE((status->'maktaba'->>'registered')::boolean, false), status,
	        requested_base_url, register_requested_at, last_error
	    FROM plex_livetv_state WHERE id = 1`).Scan(&registered, &status, &requested, &asked, &lastErr)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Integration{State: NotUsed}
	case err != nil:
		return Integration{State: Failed, Detail: err.Error()}
	}
	base := ""
	if publicStream != nil {
		base = strings.TrimRight(publicStream(), "/")
	}
	if requested.String != base && !registered {
		return Integration{State: NotUsed}
	}
	if base == "" {
		return Integration{State: NeedsYou, Detail: "this machine's TV address is not known"}
	}
	var pinned string
	_ = db.QueryRowContext(ctx, `SELECT COALESCE(value->>'tuner_url', '') FROM app_settings
	    WHERE key = 'rules.plexlive'`).Scan(&pinned)
	if strings.TrimSpace(pinned) != "" && !sameHost(pinned, base) {
		return Integration{State: NeedsYou, Detail: pinned}
	}
	if requested.String != base {
		// asked once: the stage moves the tuner and clears the request when it answers
		if _, err := db.ExecContext(ctx, `UPDATE plex_livetv_state SET register_requested_at = now(),
		    requested_base_url = $1, updated_at = now() WHERE id = 1`, base); err != nil {
			return Integration{State: Failed, Detail: err.Error()}
		}
		res, err := queues.Enqueue(ctx, db, queues.EnqueueParams{SubjectKind: "catalog", SubjectID: catalogSubject,
			Stage: plexLiveStage, Force: true, Reason: queues.ReasonFor(ctx, plexLiveRequest, nil)})
		switch {
		case err != nil:
			return Integration{State: Failed, Detail: err.Error()}
		case res.Outcome != queues.OutcomeInserted && res.Outcome != queues.OutcomeCoalesced &&
			res.Outcome != queues.OutcomeRerunRequested:
			return Integration{State: NeedsYou, Detail: "the Plex Live TV stage is not available here (" + res.Outcome + ")"}
		}
		return Integration{State: Pending, Detail: base}
	}
	if asked.Valid {
		// not answered yet (or answered with an error it retries)
		return Integration{State: Pending, Detail: lastErr.String}
	}
	var st struct {
		Error  *string `json:"error"`
		Target struct {
			TunerURL string `json:"tuner_url"`
		} `json:"target"`
		Maktaba struct {
			Registered bool `json:"registered"`
		} `json:"maktaba"`
		Actions struct {
			Register string `json:"register"`
			Move     string `json:"move"`
		} `json:"actions"`
	}
	_ = json.Unmarshal(status, &st)
	switch {
	case st.Maktaba.Registered && sameHost(st.Target.TunerURL, base):
		return Integration{State: Moved, Detail: base}
	case st.Error != nil && *st.Error != "":
		return Integration{State: Failed, Detail: *st.Error}
	default:
		return Integration{State: NeedsYou, Detail: st.Actions.Register}
	}
}

// sameHost reports whether two addresses name the same host and port (scheme and path
// aside), as the plex_livetv stage compares them.
func sameHost(a, b string) bool {
	ha, hb := hostPort(a), hostPort(b)
	return ha != "" && ha == hb
}

func hostPort(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return strings.ToLower(u.Hostname()) + ":" + port
}

// youtube: the channels' downloads that read a browser's cookies need someone signed in
// to YouTube in a browser of this machine.
func youtube(ctx context.Context, db *sql.DB) Integration {
	var browser sql.NullString
	err := db.QueryRowContext(ctx, `SELECT value->>'cookies_from_browser' FROM app_settings
	    WHERE key = 'rules.exactdates'`).Scan(&browser)
	if err != nil || strings.TrimSpace(browser.String) == "" {
		return Integration{State: NotUsed}
	}
	return Integration{State: NeedsYou, Detail: "sign in to YouTube in " + browser.String + " on this machine"}
}

// llm: a model runtime the old home ran on itself is not here unless this machine runs
// one too.
func llm(ctx context.Context, _ *sql.DB) Integration {
	addr := strings.TrimPrefix(strings.TrimPrefix(os.Getenv("MAKTABA_OLLAMA_URL"), "http://"), "https://")
	if addr == "" {
		addr = "127.0.0.1:11434"
	}
	addr = strings.TrimRight(addr, "/")
	if i := strings.Index(addr, "/"); i > 0 {
		addr = addr[:i]
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err == nil {
		_ = c.Close()
		return Integration{State: Moved, Detail: addr}
	}
	return Integration{State: NeedsYou, Detail: "no model runtime answers on this machine (" + addr + ")"}
}

// HFTokenPath is where this machine keeps the Hugging Face token (HF_HOME/token).
func HFTokenPath(stateDir string) string {
	if p := os.Getenv("HF_HOME"); p != "" {
		return filepath.Join(p, "token")
	}
	if stateDir == "" {
		return ""
	}
	return filepath.Join(stateDir, "models", "huggingface", "token")
}
