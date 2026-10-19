// Admin → Live TV → Changes (Epic 33, story 33.11): what is live, what is staged, the
// change in flight, the drafts queue and the release history.
//
//   GET /api/tv/changes?limit=100   every change, the staging settings, the authors
//   GET /api/tv/slots               the slot bar
//   GET /api/tv/releases            the release history
//
// Re-read every 2 s while visible (AC5). Stable layout (PR #76): the sections are always
// there in the same order, rows keep their order (newest first) and their objects when
// nothing changed (one batched update per poll), the skeleton shows on the first load
// only.
import { useCallback, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { AdminGate } from "../../../components/AdminGate";
import { useI18n } from "../../../lib/i18n";
import {
  IN_FLIGHT,
  reconcile,
  reconcileList,
  tvChangesApi,
  type ChangeList,
  type ChangeState,
  type Release,
  type SlotsView,
  type StagingConfig,
  type TvChange,
} from "../../../lib/tvChanges";
import {
  CHANGES_POLL_MS,
  ChecksSummary,
  Clock,
  Kinds,
  StateChip,
  actionError,
  sourceLabel,
  useTk,
  useVisiblePoll,
  userName,
} from "./changeBits";
import { SlotBar } from "./SlotBar";
import { HeldReleaseCard } from "./HeldReleaseCard";
import "../../../styles/tvchanges.css";

export function AdminTvChanges() {
  return (
    <AdminGate>
      <ChangesInner />
    </AdminGate>
  );
}

/** The list filters, always in this order. */
export const FILTERS = ["all", "drafts", "inflight", "live", "ended"] as const;
export type Filter = (typeof FILTERS)[number];

const ENDED: ReadonlySet<ChangeState> = new Set([
  "superseded",
  "failed",
  "cancelled",
  "rolled_back",
]);

export function matches(f: Filter, s: ChangeState): boolean {
  switch (f) {
    case "drafts":
      return s === "draft";
    case "inflight":
      return IN_FLIGHT.has(s);
    case "live":
      return s === "live";
    case "ended":
      return ENDED.has(s);
    default:
      return true;
  }
}

function ChangesInner() {
  const { t, n } = useI18n();
  const [list, setList] = useState<ChangeList | null>(null);
  const [slots, setSlots] = useState<SlotsView | null>(null);
  const [releases, setReleases] = useState<Release[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<Filter>("all");

  const load = useCallback(async () => {
    try {
      const [l, s, r] = await Promise.all([
        tvChangesApi.list(undefined, 100),
        tvChangesApi.slots().catch(() => null),
        tvChangesApi.releases(20).catch(() => null),
      ]);
      // one batched update: unchanged rows keep their objects
      setList((prev) =>
        reconcile(prev, { ...l, items: reconcileList(prev?.items, l.items ?? []) })
      );
      if (s) setSlots((prev) => reconcile(prev, s));
      if (r) setReleases((prev) => reconcile(prev, r.items ?? []));
      setError(null);
    } catch (e) {
      setError(actionError(t, e));
    }
  }, [t]);

  useVisiblePoll(load, CHANGES_POLL_MS);

  const items = useMemo(() => list?.items ?? [], [list]);
  const inFlight = useMemo(
    () => items.find((c) => c.id === list?.in_flight) ?? null,
    [items, list?.in_flight]
  );
  const shown = useMemo(() => items.filter((c) => matches(filter, c.state)), [items, filter]);
  const counts = useMemo(() => {
    const out: Record<Filter, number> = { all: 0, drafts: 0, inflight: 0, live: 0, ended: 0 };
    for (const c of items) for (const f of FILTERS) if (matches(f, c.state)) out[f] += 1;
    return out;
  }, [items]);

  const cfg = list?.config;

  return (
    <section className="mkt-page mkt-tvx">
      <header className="mkt-page__header">
        <h1>{t("tvchanges.title")}</h1>
        <Link to="/admin/live-tv">{t("tvchanges.backToLiveTv")}</Link>
      </header>
      <p className="mkt-tvx-hint">{t("tvchanges.intro")}</p>
      {/* Epic 35: a release that waits for an admin's approval */}
      <HeldReleaseCard />

      <div className="mkt-tvx-status" aria-live="polite">
        {error && (
          <p role="alert" className="mkt-tvx-error">
            {t("tvchanges.loadError", { error })}
          </p>
        )}
      </div>

      {cfg && !cfg.enabled && (
        <div className="mkt-tvx-banner" role="note" data-testid="staging-off">
          <strong>{t("tvchanges.off.title")}</strong> {t("tvchanges.off.body")}{" "}
          <Link to="/settings">{t("tvchanges.openSettings")}</Link>
        </div>
      )}

      <SlotBar view={slots} inFlight={inFlight} />

      <div className="mkt-tvx-banner-slot">
        {inFlight ? (
          <Link
            to={`/admin/live-tv/changes/${inFlight.id}`}
            className="mkt-tvx-banner mkt-tvx-banner--live"
            data-testid="in-flight"
          >
            {t("tvchanges.inFlight", {
              n: n(inFlight.number),
              state: t(`tvchanges.state.${inFlight.state}`),
              title: inFlight.title || t("tvchanges.untitled"),
            })}
          </Link>
        ) : (
          <p className="mkt-tvx-banner mkt-tvx-banner--quiet">
            {list ? t("tvchanges.noneInFlight") : " "}
          </p>
        )}
      </div>

      {cfg?.enabled !== false && (
        <div className="mkt-tvx-card">
          <div className="mkt-tvx-filters" role="group" aria-label={t("tvchanges.filter.label")}>
            {FILTERS.map((f) => (
              <button
                key={f}
                type="button"
                className={`mkt-tvx-filter${filter === f ? " mkt-tvx-filter--on" : ""}`}
                aria-pressed={filter === f}
                onClick={() => setFilter(f)}
              >
                {t(`tvchanges.filter.${f}`)} <span className="mkt-tvx-num">{n(counts[f])}</span>
              </button>
            ))}
          </div>
          {!list ? (
            <div className="mkt-tvx-skeleton" aria-hidden="true" />
          ) : shown.length === 0 ? (
            <p className="mkt-tvx-hint">{t(`tvchanges.empty.${filter}`)}</p>
          ) : (
            <ChangeTable items={shown} users={list.users} />
          )}
        </div>
      )}

      {cfg && <SettingsPanel cfg={cfg} />}

      <div className="mkt-tvx-card">
        <h2>{t("tvchanges.releases.title")}</h2>
        {!releases ? (
          <div className="mkt-tvx-skeleton mkt-tvx-skeleton--short" aria-hidden="true" />
        ) : releases.length === 0 ? (
          <p className="mkt-tvx-hint">{t("tvchanges.releases.none")}</p>
        ) : (
          <ul className="mkt-tvx-releases">
            {releases.map((r) => (
              <li key={r.id}>
                <span className="mkt-tvx-mono">{r.id.slice(0, 8)}</span>
                <span>{t("tvchanges.releases.gen", { n: n(r.sched_gen) })}</span>
                <Kinds kinds={r.kinds} />
                <span className="mkt-tvx-muted">{r.streaming_version || "—"}</span>
                <Clock at={r.created_at} />
                {r.live && (
                  <span className="mkt-tvx-chip mkt-tvx-chip--success">
                    {t("tvchanges.releases.live")}
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}

function ChangeTable({ items, users }: { items: TvChange[]; users: Record<string, string> }) {
  const { t, n, formatDate } = useI18n();
  const tk = useTk();
  return (
    <div className="mkt-tvx-table" role="table" aria-label={t("tvchanges.list")}>
      <div className="mkt-tvx-row mkt-tvx-row--head" role="row">
        <span role="columnheader">#</span>
        <span role="columnheader">{t("tvchanges.col.change")}</span>
        <span role="columnheader">{t("tvchanges.col.state")}</span>
        <span role="columnheader">{t("tvchanges.col.kinds")}</span>
        <span role="columnheader">{t("tvchanges.col.source")}</span>
        <span role="columnheader">{t("tvchanges.col.checks")}</span>
        <span role="columnheader">{t("tvchanges.col.swap")}</span>
      </div>
      {items.map((c) => (
        <div className="mkt-tvx-row" role="row" key={c.id} data-testid={`change-${c.number}`}>
          <span role="cell" className="mkt-tvx-num">
            {n(c.number)}
          </span>
          <span role="cell" className="mkt-tvx-row__title">
            <Link to={`/admin/live-tv/changes/${c.id}`}>{c.title || t("tvchanges.untitled")}</Link>
            <span className="mkt-tvx-muted">
              {t("tvchanges.byAt", {
                who: userName(users, c.created_by, t("tvchanges.system")),
                at: formatDate(c.created_at),
              })}
            </span>
          </span>
          <span role="cell">
            <StateChip state={c.state} />
          </span>
          <span role="cell">
            <Kinds kinds={c.kinds} />
          </span>
          <span role="cell">{sourceLabel(tk, c)}</span>
          <span role="cell">
            <ChecksSummary report={c.verify_report} />
          </span>
          <span role="cell" className="mkt-tvx-row__swap">
            {c.swap_at ? (
              <Clock at={c.swap_at} />
            ) : (
              <span className="mkt-tvx-muted">{c.swapped_at ? formatDate(c.swapped_at) : "—"}</span>
            )}
          </span>
        </div>
      ))}
    </div>
  );
}

/** The staging settings next to the list (quiet window, auto-promote rules). */
function SettingsPanel({ cfg }: { cfg: StagingConfig }) {
  const { t, n } = useI18n();
  const tk = useTk();
  const kinds = Object.keys(cfg.auto_promote ?? {}).sort();
  return (
    <details className="mkt-tvx-card mkt-tvx-settings" data-testid="staging-settings">
      <summary>
        <h2>{t("tvchanges.settings.title")}</h2>
      </summary>
      <dl className="mkt-tvx-facts">
        <dt>{t("tvchanges.settings.enabled")}</dt>
        <dd>{cfg.enabled ? t("tvchanges.on") : t("tvchanges.off.short")}</dd>
        <dt>{t("tvchanges.settings.swapWait")}</dt>
        <dd>
          {t(`tvchanges.swapWait.${cfg.swap_wait}`)}
          {cfg.swap_wait === "boundary" &&
            ` · ${t("tvchanges.settings.maxWait", { n: n(cfg.boundary_max_wait_minutes) })}`}
        </dd>
        <dt>{t("tvchanges.settings.quietWindow")}</dt>
        <dd>
          <span dir="ltr">{cfg.quiet_window}</span>
          {" · "}
          {t("tvchanges.settings.quietViewers", { n: n(cfg.quiet_max_viewers) })}
        </dd>
        <dt>{t("tvchanges.settings.secondAdmin")}</dt>
        <dd>{cfg.require_second_admin ? t("tvchanges.on") : t("tvchanges.off.short")}</dd>
        <dt>{t("tvchanges.settings.keepWarm")}</dt>
        <dd>
          {t("tvchanges.settings.keepWarmValue", {
            min: n(cfg.keep_warm_minutes),
            hours: n(cfg.rollback_retention_hours),
          })}
        </dd>
        <dt>{t("tvchanges.settings.autoPromote")}</dt>
        <dd>
          <ul className="mkt-tvx-rules">
            {kinds.map((k) => (
              <li key={k}>
                <span>{tk(`tvchanges.kind.${k}`, k)}</span>
                <span className="mkt-tvx-muted">{t(`tvchanges.rule.${cfg.auto_promote[k]}`)}</span>
              </li>
            ))}
          </ul>
        </dd>
      </dl>
      <p className="mkt-tvx-hint">
        <Link to="/settings">{t("tvchanges.openSettings")}</Link>
      </p>
    </details>
  );
}
