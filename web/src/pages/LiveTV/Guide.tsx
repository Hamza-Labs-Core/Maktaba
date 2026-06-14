// Story 27.6 — EPG grid UI.
//
// The classic cable-TV channel × time guide. Rows are channels, the
// horizontal axis is time, each cell is a program sized proportional to
// its duration. A now-line marks the current wall-clock moment; clicking
// the airing cell tunes the channel (→ the live player, 27.7) while a
// future cell opens its details. A category filter narrows the lineup and
// a "What's On Now" compact toggle (the default on mobile) lists each
// channel's current program as cards.
//
// Wall-clock truth: every "is airing"/progress/now-line value is derived
// from `now` against the absolute `start_at`/`end_at` the stream itself
// uses, with `now` reconciled to the guide payload's `server_time` so a
// wrong client clock can't misplace the line (EC4).
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Modal } from "@ds/components/Modal/Modal";
import { Badge } from "@ds/components/Badge/Badge";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { useI18n } from "../../lib/i18n";
import {
  channelsApi,
  isAiring,
  isFiller,
  progressFraction,
  type GuideResponse,
  type Program,
} from "../../lib/channels";

// Pixels per minute on the time axis. The ratio between cells is what AC1
// guarantees, so the absolute value is cosmetic — 4px/min gives a 1-hour
// column of 240px and keeps a 30-min vs 120-min pair at the spec's 1:4.
const PX_PER_MIN = 4;
// Guide window: now − 30 min … now + 3 h (AC1).
const WINDOW_BACK_MIN = 30;
const WINDOW_FWD_MIN = 180;
const REFRESH_MS = 30_000;

function minutesBetween(a: number, b: number): number {
  return (b - a) / 60_000;
}

// collapseFiller merges runs of filler/bumper blocks into one cell so the
// grid isn't shredded into slivers (EC2 / 27.4 AC10) — the merged cell
// reads as an "Up Next" placeholder.
function collapseFiller(programs: Program[]): Program[] {
  const out: Program[] = [];
  for (const p of programs) {
    const prev = out[out.length - 1];
    if (isFiller(p.kind) && prev && isFiller(prev.kind)) {
      out[out.length - 1] = { ...prev, end_at: p.end_at };
    } else {
      out.push(p);
    }
  }
  return out;
}

export function Guide() {
  const { t, formatDate } = useI18n();
  const nav = useNavigate();
  const [guide, setGuide] = useState<GuideResponse | null>(null);
  const [err, setErr] = useState(false);
  const [category, setCategory] = useState("");
  const [compact, setCompact] = useState(() =>
    typeof window !== "undefined" ? window.matchMedia?.("(max-width: 640px)").matches : false
  );
  const [details, setDetails] = useState<Program | null>(null);
  // Server-clock offset (serverTime − clientTime at fetch) so the now-line
  // tracks server truth, not the device clock (EC4).
  const offsetRef = useRef(0);
  const [, tick] = useState(0);

  const load = useCallback(() => {
    const now = Date.now();
    const start = new Date(now - WINDOW_BACK_MIN * 60_000).toISOString();
    const end = new Date(now + WINDOW_FWD_MIN * 60_000).toISOString();
    channelsApi
      .guide({ start, end, category: category || undefined })
      .then((g) => {
        const srv = Date.parse(g.server_time);
        if (!Number.isNaN(srv)) offsetRef.current = srv - Date.now();
        setGuide(g);
        setErr(false);
      })
      .catch(() => setErr(true));
  }, [category]);

  useEffect(() => {
    load();
    const refresh = setInterval(load, REFRESH_MS);
    // 1s tick advances the now-line + progress without a refetch (AC2/AC8).
    const beat = setInterval(() => tick((n) => n + 1), 1000);
    return () => {
      clearInterval(refresh);
      clearInterval(beat);
    };
  }, [load]);

  const nowMs = Date.now() + offsetRef.current;
  const windowStart = nowMs - WINDOW_BACK_MIN * 60_000;
  const totalMin = WINDOW_BACK_MIN + WINDOW_FWD_MIN;
  const gridWidth = totalMin * PX_PER_MIN;
  const nowX = WINDOW_BACK_MIN * PX_PER_MIN;

  const categories = useMemo(() => {
    const set = new Set<string>();
    guide?.channels.forEach((c) => c.channel.category && set.add(c.channel.category));
    return [...set].sort();
  }, [guide]);

  // Hour ticks for the time axis header.
  const hourTicks = useMemo(() => {
    const ticks: Array<{ x: number; label: string }> = [];
    const first = new Date(windowStart);
    first.setMinutes(0, 0, 0);
    for (let ms = first.getTime(); ms < windowStart + totalMin * 60_000; ms += 3_600_000) {
      if (ms < windowStart) continue;
      ticks.push({
        x: minutesBetween(windowStart, ms) * PX_PER_MIN,
        label: new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
      });
    }
    return ticks;
  }, [windowStart, totalMin]);

  function onCellActivate(p: Program, channelNumber: number) {
    // The airing block tunes live; a future block opens details (AC3).
    if (isAiring(p, nowMs) && !isFiller(p.kind)) {
      nav(`/live/${channelNumber}`);
    } else {
      setDetails(p);
    }
  }

  if (err && !guide) {
    return (
      <section className="mkt-page">
        <ErrorState kind="server" title={t("common.error")} description={t("guide.loadError")} />
      </section>
    );
  }
  if (!guide) return <p className="mkt-loading">{t("common.loading")}</p>;

  const channels = guide.channels;

  return (
    <section className="mkt-page mkt-guide">
      <header className="mkt-page__header">
        <h1>{t("guide.title")}</h1>
        <div className="mkt-toolbar" role="toolbar" aria-label={t("guide.toolbar")}>
          <label className="mkt-field">
            <span className="mkt-field__label">{t("guide.category")}</span>
            <select
              value={category}
              aria-label={t("guide.category")}
              onChange={(e) => setCategory(e.target.value)}
            >
              <option value="">{t("guide.allCategories")}</option>
              {categories.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
          </label>
          <Button variant="secondary" aria-pressed={compact} onClick={() => setCompact((v) => !v)}>
            {compact ? t("guide.viewGrid") : t("guide.viewNow")}
          </Button>
        </div>
      </header>

      {channels.length === 0 ? (
        <EmptyState
          title={t("guide.empty.title")}
          description={t("guide.empty.desc")}
          action={<Button onClick={() => nav("/admin/channels")}>{t("guide.empty.cta")}</Button>}
        />
      ) : compact ? (
        <WhatsOnNowList channels={channels} nowMs={nowMs} onTune={(num) => nav(`/live/${num}`)} />
      ) : (
        <div className="mkt-guide__scroll" data-testid="guide-grid">
          <div className="mkt-guide__timeaxis" style={{ width: gridWidth }}>
            {hourTicks.map((tk) => (
              <span key={tk.x} className="mkt-guide__tick" style={{ insetInlineStart: tk.x }}>
                {tk.label}
              </span>
            ))}
            <span
              className="mkt-guide__nowline mkt-guide__nowline--axis"
              data-testid="now-line"
              style={{ insetInlineStart: nowX }}
              aria-hidden="true"
            />
          </div>
          {channels.map(({ channel, programs }) => {
            const cells = collapseFiller(programs);
            return (
              <div className="mkt-guide__row" key={channel.id}>
                <div className="mkt-guide__chanhead">
                  <span className="mkt-guide__channum">{channel.number}</span>
                  <span className="mkt-guide__channame">{channel.name}</span>
                </div>
                <div className="mkt-guide__track" style={{ width: gridWidth }}>
                  {cells.length === 0 && (
                    <div
                      className="mkt-guide__cell mkt-guide__cell--empty"
                      style={{ width: gridWidth }}
                    >
                      {t("guide.noContent")}
                    </div>
                  )}
                  {cells.map((p) => {
                    const offMin = Math.max(0, minutesBetween(windowStart, Date.parse(p.start_at)));
                    const durMin = minutesBetween(Date.parse(p.start_at), Date.parse(p.end_at));
                    const airing = isAiring(p, nowMs);
                    const prog = progressFraction(p, nowMs);
                    return (
                      <button
                        type="button"
                        key={`${p.seq}-${p.start_at}`}
                        className={`mkt-guide__cell${airing ? " mkt-guide__cell--airing" : ""}${isFiller(p.kind) ? " mkt-guide__cell--filler" : ""}`}
                        data-testid="guide-cell"
                        title={p.title}
                        style={{
                          insetInlineStart: offMin * PX_PER_MIN,
                          width: Math.max(2, durMin * PX_PER_MIN),
                        }}
                        onClick={() => onCellActivate(p, channel.number)}
                      >
                        <span className="mkt-guide__celltitle">
                          {isFiller(p.kind) ? t("guide.upNext") : p.title}
                        </span>
                        {airing && prog !== null && (
                          <span
                            className="mkt-guide__cellprogress"
                            style={{ inlineSize: `${prog * 100}%` }}
                            aria-hidden="true"
                          />
                        )}
                      </button>
                    );
                  })}
                </div>
              </div>
            );
          })}
          <div
            className="mkt-guide__nowline"
            style={{ insetInlineStart: nowX + 160 /* channel head gutter */ }}
            aria-hidden="true"
          />
        </div>
      )}

      <Modal open={details !== null} onClose={() => setDetails(null)} title={details?.title ?? ""}>
        {details && (
          <div className="mkt-guide__details">
            {details.poster_url && (
              <img className="mkt-guide__poster" src={details.poster_url} alt="" />
            )}
            <dl>
              <div>
                <dt>{t("guide.startsAt")}</dt>
                <dd>{formatDate(details.start_at)}</dd>
              </div>
              {details.series && (
                <div>
                  <dt>{t("guide.series")}</dt>
                  <dd>
                    {details.series}
                    {details.episode ? ` · ${details.episode}` : ""}
                  </dd>
                </div>
              )}
              <div>
                <dt>{t("guide.duration")}</dt>
                <dd>
                  {Math.round(
                    minutesBetween(Date.parse(details.start_at), Date.parse(details.end_at))
                  )}{" "}
                  {t("guide.minutes")}
                </dd>
              </div>
            </dl>
            {details.description && <p>{details.description}</p>}
            <Badge tone="neutral">{details.kind}</Badge>
          </div>
        )}
      </Modal>
    </section>
  );
}

// WhatsOnNowList — the compact, vertical "now" view (AC5/AC6), the mobile
// default. Each channel becomes a card with its current program + progress.
function WhatsOnNowList({
  channels,
  nowMs,
  onTune,
}: {
  channels: GuideResponse["channels"];
  nowMs: number;
  onTune: (channelNumber: number) => void;
}) {
  const { t } = useI18n();
  return (
    <div className="mkt-guide__nowgrid" data-testid="whats-on-now">
      {channels.map(({ channel, programs }) => {
        const current = programs.find((p) => isAiring(p, nowMs));
        const next = programs.find((p) => Date.parse(p.start_at) > nowMs);
        const prog = current ? progressFraction(current, nowMs) : null;
        return (
          <Card key={channel.id} className="mkt-nowcard">
            <button
              type="button"
              className="mkt-nowcard__btn"
              onClick={() => onTune(channel.number)}
            >
              <div className="mkt-nowcard__head">
                <span className="mkt-guide__channum">{channel.number}</span>
                <span className="mkt-guide__channame">{channel.name}</span>
              </div>
              <strong className="mkt-nowcard__prog">
                {current ? current.title : t("guide.noContent")}
              </strong>
              {prog !== null && <ProgressBar value={prog * 100} label={t("guide.duration")} />}
              {next && (
                <span className="mkt-nowcard__next">
                  {t("guide.nextLabel")}: {next.title}
                </span>
              )}
            </button>
          </Card>
        );
      })}
    </div>
  );
}
