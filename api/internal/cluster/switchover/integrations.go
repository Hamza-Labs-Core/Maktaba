package switchover

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Integration states.
const (
	Moved    = "moved"
	Failed   = "failed"
	NeedsYou = "needs_you"
	NotUsed  = "not_used"
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
//   - plex_live_tv: Plex is asked again for this home's tuner and guide (the stage
//     re-registers them at this machine's address);
//   - hugging_face: the token came with the kit;
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
		out["youtube"] = youtube(ctx, db)
		out["llm"] = llm(ctx, db)
		return out
	}
}

// liveTV re-registers Plex's tuner and guide at this machine's address when the old
// home had registered them (the plex_livetv stage moves the tuner: it finds it by its
// HDHomeRun id and points it here).
func liveTV(ctx context.Context, db *sql.DB, publicStream func() string) Integration {
	var registered bool
	var requested sql.NullString
	err := db.QueryRowContext(ctx, `SELECT COALESCE((status->'maktaba'->>'registered')::boolean, false), requested_base_url
	    FROM plex_livetv_state WHERE id = 1`).Scan(&registered, &requested)
	switch {
	case errors.Is(err, sql.ErrNoRows), err == nil && !registered:
		return Integration{State: NotUsed}
	case err != nil:
		return Integration{State: Failed, Detail: err.Error()}
	}
	base := ""
	if publicStream != nil {
		base = publicStream()
	}
	if requested.String == base && base != "" {
		return Integration{State: Moved, Detail: base}
	}
	if _, err := db.ExecContext(ctx, `UPDATE plex_livetv_state SET register_requested_at = now(),
	    requested_base_url = NULLIF($1, ''), updated_at = now() WHERE id = 1`, base); err != nil {
		return Integration{State: Failed, Detail: err.Error()}
	}
	return Integration{State: Moved, Detail: base}
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
