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
import { Link, useNavigate } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Modal } from "@ds/components/Modal/Modal";
import { Badge } from "@ds/components/Badge/Badge";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { LoadError } from "../../components/LoadError";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { useI18n } from "../../lib/i18n";
import { useOptionalAuth } from "../../lib/auth";
import { Skeleton } from "@ds/components/Skeleton/Skeleton";
import { Loading } from "../../components/Placeholders";
import { cachedAnswer, keepAnswer } from "../../lib/pageCache";
import {
  channelLogo,
  channelsApi,
  isAiring,
  isFiller,
  progressFraction,
  type GuideResponse,
  type Program,
} from "../../lib/channels";
import { TitleRow } from "../../help/HelpLink";
import { SERVER_PAGES } from "../../lib/edition";

// Guide window: two hours, now − 30 min … now + 90 min (the owner's choice,
// 2026-10-27: what is on now and next, not a long scroll).
const WINDOW_BACK_MIN = 30;
const WINDOW_FWD_MIN = 90;
const WINDOW_MIN = WINDOW_BACK_MIN + WINDOW_FWD_MIN;
// Half-hour ticks on the time axis: four across the two hours.
const TICK_MIN = 30;
// A tick needs this many minutes before the window's end to show its label.
const TICK_LABEL_MIN = 8;
const REFRESH_MS = 30_000;

function minutesBetween(a: number, b: number): number {
  return (b - a) / 60_000;
}

// pct places a span of minutes on the time axis as a share of the window.
// The window always fills the guide's width beside the channel column, so
// cells grow and shrink with the screen while durations keep their ratio (a
// 30-min cell is a quarter of a 120-min one at any width).
function pct(minutes: number): string {
  return `${(minutes / WINDOW_MIN) * 100}%`;
}

interface CellBox {
  /** minutes from the window start to the cell's visible start */
  start: number;
  /** the cell's visible minutes */
  width: number;
  /** the programme began before the window: the cell is cut at its start */
  cutStart: boolean;
  /** the programme runs past the window: the cell is cut at its end */
  cutEnd: boolean;
}

// cellBox places a program on the time axis, clipped to the window: one that
// started earlier begins at the window's start edge and one that runs on
// stops at its end edge, so nothing spills out of the grid. A programme
// wholly outside the window has no box (null). A block with unparseable or
// backwards timestamps gets a zero-width box at the window start (the CSS
// minimum keeps it visible) instead of a NaN/negative width, which React
// drops, leaving an unsized cell.
function cellBox(p: Program, windowStart: number): CellBox | null {
  const s = Date.parse(p.start_at);
  const e = Date.parse(p.end_at);
  if (Number.isNaN(s) || Number.isNaN(e) || e <= s) {
    return { start: 0, width: 0, cutStart: false, cutEnd: false };
  }
  const windowEnd = windowStart + WINDOW_MIN * 60_000;
  if (e <= windowStart || s >= windowEnd) return null;
  const from = Math.max(s, windowStart);
  const to = Math.min(e, windowEnd);
  return {
    start: minutesBetween(windowStart, from),
    width: minutesBetween(from, to),
    cutStart: s < windowStart,
    cutEnd: e > windowEnd,
  };
}

// visibleProgress is how far `now` is through the visible part of an airing
// cell, so its progress bar ends on the now-line even when the programme
// started before the window.
function visibleProgress(box: CellBox, windowStart: number, nowMs: number): number {
  if (box.width <= 0) return 0;
  const elapsed = minutesBetween(windowStart, nowMs) - box.start;
  return Math.min(1, Math.max(0, elapsed / box.width));
}

// collapseFiller merges runs of filler/bumper blocks into one cell so the
// grid isn't shredded into slivers (EC2 / 27.4 AC10) — the merged cell
// reads as the first block's title (the API's filler title, rules
// tv.guide_filler_title), else an "Up next" placeholder.
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
  // only admins can create channels: the empty-state CTA is theirs
  const me = useOptionalAuth()?.user;
  const isAdmin = me?.is_admin === true;
  const userId = me?.id ?? null;
  // a return shows the last guide at once (lib/pageCache) while it reads the
  // current window; the guide refreshes itself on its own clock after that
  const [guide, setGuide] = useState<GuideResponse | null>(
    () => cachedAnswer<GuideResponse>(userId, "guide:") ?? null
  );
  const [err, setErr] = useState<unknown>(null);
  const [category, setCategory] = useState("");
  const [compact, setCompact] = useState(() =>
    // `?? false`: without matchMedia this was undefined and the toggle lost aria-pressed
    typeof window !== "undefined"
      ? (window.matchMedia?.("(max-width: 640px)").matches ?? false)
      : false
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
        keepAnswer(userId, `guide:${category}`, g);
        setGuide(g);
        setErr(null);
      })
      .catch((e: unknown) => setErr(e));
  }, [category, userId]);

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
  // the now-line's share of the window: a quarter of the way in
  const nowFraction = WINDOW_BACK_MIN / WINDOW_MIN;

  const categories = useMemo(() => {
    const set = new Set<string>();
    guide?.channels.forEach((c) => c.channel.category && set.add(c.channel.category));
    return [...set].sort();
  }, [guide]);

  // Half-hour ticks for the time axis header. Each label starts at its mark
  // and reads toward the window's end, so a tick too close to that end to
  // show its label is left out rather than cut.
  const ticks = useMemo(() => {
    const out: Array<{ ms: number; x: string; half: boolean; label: string }> = [];
    const first = new Date(windowStart);
    first.setSeconds(0, 0);
    first.setMinutes(Math.ceil(first.getMinutes() / TICK_MIN) * TICK_MIN);
    if (first.getTime() < windowStart) first.setMinutes(first.getMinutes() + TICK_MIN);
    const lastLabel = windowStart + (WINDOW_MIN - TICK_LABEL_MIN) * 60_000;
    for (let ms = first.getTime(); ms <= lastLabel; ms += TICK_MIN * 60_000) {
      const at = new Date(ms);
      out.push({
        ms,
        x: pct(minutesBetween(windowStart, ms)),
        half: at.getMinutes() !== 0,
        label: at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
      });
    }
    return out;
  }, [windowStart]);

  function onCellActivate(p: Program, channelNumber: number) {
    // The airing block tunes live; a future block opens details (AC3).
    if (isAiring(p, nowMs) && !isFiller(p.kind)) {
      nav(`/live/${channelNumber}`);
    } else {
      setDetails(p);
    }
  }

  if (err != null && !guide) {
    return (
      <section className="mkt-page">
        <LoadError error={err} title={t("guide.loadError")} onRetry={load} />
      </section>
    );
  }
  if (!guide) {
    return (
      <section className="mkt-page mkt-guide">
        <header className="mkt-page__header">
          <TitleRow>
            <h1>{t("guide.title")}</h1>
          </TitleRow>
        </header>
        <Loading loading>
          <div className="mkt-guide__scroll" aria-hidden="true">
            {Array.from({ length: 5 }, (_, i) => (
              <div className="mkt-guide__row" key={i}>
                <span className="mkt-guide__chanhead">
                  <Skeleton variant="text" className="mkt-ph-line" width="70%" />
                </span>
                <div className="mkt-guide__track">
                  <Skeleton variant="rect" className="mkt-ph-full" />
                </div>
              </div>
            ))}
          </div>
        </Loading>
      </section>
    );
  }

  const channels = guide.channels;

  return (
    <section className="mkt-page mkt-guide">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("guide.title")}</h1>
        </TitleRow>
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
          action={
            SERVER_PAGES && isAdmin ? (
              <Button onClick={() => nav("/admin/channels")}>{t("guide.empty.cta")}</Button>
            ) : undefined
          }
        />
      ) : compact ? (
        <WhatsOnNowList channels={channels} nowMs={nowMs} onTune={(num) => nav(`/live/${num}`)} />
      ) : (
        <div className="mkt-guide__scroll" data-testid="guide-grid">
          <div className="mkt-guide__timeaxis" data-testid="guide-timeaxis">
            {ticks.map((tk) => (
              <span
                key={tk.ms}
                className={`mkt-guide__tick${tk.half ? " mkt-guide__tick--half" : ""}`}
                data-testid="guide-tick"
                style={{ insetInlineStart: tk.x }}
              >
                {/* times read left to right in Arabic too */}
                <span dir="ltr">{tk.label}</span>
              </span>
            ))}
            <span
              className="mkt-guide__nowline mkt-guide__nowline--axis"
              data-testid="now-line"
              style={{ insetInlineStart: pct(WINDOW_BACK_MIN) }}
              aria-hidden="true"
            />
          </div>
          {channels.map(({ channel, programs }) => {
            const cells = collapseFiller(programs).flatMap((p) => {
              const box = cellBox(p, windowStart);
              return box ? [{ p, box }] : [];
            });
            return (
              <div className="mkt-guide__row" key={channel.id}>
                <Link
                  className="mkt-guide__chanhead"
                  to={`/live/${channel.number}`}
                  aria-label={t("guide.watchChannel", {
                    number: channel.number,
                    name: channel.name,
                  })}
                >
                  <ChannelLogo src={channelLogo(channel)} />
                  <span className="mkt-guide__channum">{channel.number}</span>
                  {/* dir=auto: an English name in Arabic (or the reverse) is cut at its own end */}
                  <span className="mkt-guide__channame" dir="auto">
                    {channel.name}
                  </span>
                </Link>
                <div className="mkt-guide__track">
                  {cells.length === 0 && (
                    <div
                      className="mkt-guide__cell mkt-guide__cell--empty"
                      style={{ width: "100%" }}
                    >
                      {t("guide.noContent")}
                    </div>
                  )}
                  {cells.map(({ p, box }) => {
                    const airing = isAiring(p, nowMs);
                    // progressFraction keeps a bad block bar-less; the bar
                    // itself spans the visible part, up to the now-line
                    const prog =
                      progressFraction(p, nowMs) === null
                        ? null
                        : visibleProgress(box, windowStart, nowMs);
                    const classes = ["mkt-guide__cell"];
                    if (airing) classes.push("mkt-guide__cell--airing");
                    if (isFiller(p.kind)) classes.push("mkt-guide__cell--filler");
                    if (box.cutStart) classes.push("mkt-guide__cell--cut-start");
                    if (box.cutEnd) classes.push("mkt-guide__cell--cut-end");
                    return (
                      <button
                        type="button"
                        key={`${p.seq}-${p.start_at}`}
                        className={classes.join(" ")}
                        data-testid="guide-cell"
                        title={p.title}
                        style={{
                          insetInlineStart: pct(box.start),
                          width: pct(box.width),
                        }}
                        onClick={() => onCellActivate(p, channel.number)}
                      >
                        {box.cutStart && (
                          <span
                            className="mkt-guide__cellcut mkt-guide__cellcut--start"
                            data-testid="cell-cut-start"
                            aria-hidden="true"
                          />
                        )}
                        <span className="mkt-guide__celltitle" dir="auto">
                          {isFiller(p.kind) ? p.title || t("guide.upNext") : p.title}
                        </span>
                        {box.cutEnd && (
                          <span
                            className="mkt-guide__cellcut mkt-guide__cellcut--end"
                            data-testid="cell-cut-end"
                            aria-hidden="true"
                          />
                        )}
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
            // past the channel column, then the now share of what is left
            style={{
              insetInlineStart: `calc(var(--mkt-guide-head) + (100% - var(--mkt-guide-head)) * ${nowFraction})`,
            }}
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
                <ChannelLogo src={channelLogo(channel)} />
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

// ChannelLogo shows a channel's logo, hiding itself if the image fails
// (a moved file, an unreachable URL) so the row keeps its number + name.
function ChannelLogo({ src }: { src: string | null }) {
  const [broken, setBroken] = useState(false);
  if (!src || broken) return null;
  return (
    <img
      className="mkt-guide__logo"
      src={src}
      alt=""
      loading="lazy"
      data-testid="channel-logo"
      onError={() => setBroken(true)}
    />
  );
}
