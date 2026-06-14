// Story 27.9 — "What's On Now" home rail.
//
// A horizontal rail on the home screen showing each enabled, accessible
// channel's currently-airing program (poster + progress) and the next one
// up, with a "Tune In" action that jumps straight to the live player
// (27.7). It is powered entirely by the cheap `GET /api/channels/now`
// read path — rendering the home screen starts NO transcode (AC3).
//
// The rail hides itself entirely when there are no accessible channels so
// the home screen never shows a placeholder for a feature the operator
// hasn't set up (AC5/EC5). `now`'s `server_time` reconciles progress
// against the device clock (EC2).
import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Card } from "@ds/components/Card/Card";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { useI18n } from "../lib/i18n";
import { channelsApi, progressFraction, type NowEntry } from "../lib/channels";

// Cap how many channels appear inline; the rest are reached via the guide
// (AC7). Kept small so a 50-channel lineup doesn't flood the home screen.
const RAIL_CAP = 12;
const REFRESH_MS = 30_000;

export function WhatsOnNow() {
  const { t } = useI18n();
  const nav = useNavigate();
  const [entries, setEntries] = useState<NowEntry[] | null>(null);
  const offsetRef = useRef(0);
  const [, tick] = useState(0);

  const load = useCallback(() => {
    channelsApi
      .now()
      .then((r) => {
        const srv = Date.parse(r?.server_time ?? "");
        if (!Number.isNaN(srv)) offsetRef.current = srv - Date.now();
        // Defensive: only keep well-formed entries so a malformed/empty
        // payload never crashes the home screen — it just hides the rail.
        const items = Array.isArray(r?.items)
          ? r.items.filter((e) => e && e.channel && typeof e.channel.number === "number")
          : [];
        // Lineup order (AC7); enabled+accessible is enforced server-side.
        setEntries(items.sort((a, b) => a.channel.number - b.channel.number));
      })
      .catch(() => setEntries([]));
  }, []);

  useEffect(() => {
    load();
    const refresh = setInterval(load, REFRESH_MS);
    const beat = setInterval(() => tick((x) => x + 1), 1000);
    return () => {
      clearInterval(refresh);
      clearInterval(beat);
    };
  }, [load]);

  // Hidden entirely while loading-empty or when there are no channels.
  if (!entries || entries.length === 0) return null;

  const nowMs = Date.now() + offsetRef.current;
  const shown = entries.slice(0, RAIL_CAP);
  const overflow = entries.length > RAIL_CAP;

  return (
    <section className="mkt-won" aria-label={t("won.title")} data-testid="whats-on-now-rail">
      <header className="mkt-won__head">
        <h2>{t("won.title")}</h2>
        {overflow && (
          <Link className="mkt-won__seeall" to="/guide">
            {t("won.seeAll")}
          </Link>
        )}
      </header>
      <ul className="mkt-won__rail">
        {shown.map((e) => {
          const cur = e.current ?? null;
          const prog = cur ? progressFraction(cur, nowMs) : null;
          return (
            <li key={e.channel.id} className="mkt-won__item">
              <Card interactive className="mkt-won__card">
                <button
                  type="button"
                  className="mkt-won__btn"
                  onClick={() => nav(`/live/${e.channel.number}`)}
                  aria-label={t("won.tuneIn", { name: e.channel.name })}
                >
                  <div className="mkt-won__poster">
                    {cur?.poster_url ? (
                      <img src={cur.poster_url} alt="" />
                    ) : (
                      <span className="mkt-won__poster-empty" aria-hidden="true" />
                    )}
                    {e.channel.logo_path && (
                      <img className="mkt-won__logo" src={e.channel.logo_path} alt="" />
                    )}
                  </div>
                  <div className="mkt-won__meta">
                    <span className="mkt-won__chan">
                      <span className="mkt-guide__channum">{e.channel.number}</span>
                      {e.channel.name}
                    </span>
                    <strong className="mkt-won__prog">
                      {cur ? cur.title : t("won.noContent")}
                    </strong>
                    {prog !== null && (
                      <ProgressBar value={prog * 100} label={t("guide.duration")} />
                    )}
                    {e.next && (
                      <span className="mkt-won__next">
                        {t("guide.nextLabel")}: {e.next.title}
                      </span>
                    )}
                  </div>
                  <span className="mkt-won__cta">{t("won.tune")}</span>
                </button>
              </Card>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
