// Admin → Live TV → Changes → a change (Epic 33, story 33.11): what exactly it changes
// (Lineup, Schedules, Settings, Binary & art), whether it passed its checks (on the
// staging slot, story 33.5), a preview of the staging channels (story 33.6), its history, and the actions its state allows (allowed_actions: the page
// never re-implements the state machine).
//
// Re-read every 2 s while visible (AC5); the header, the action bar (fixed order) and the
// tabs stay where they are, values change in place.
import { useCallback, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Tabs } from "@ds/components/Tabs/Tabs";
import { Tooltip } from "@ds/components/Tooltip/Tooltip";
import { AdminGate } from "../../../components/AdminGate";
import { useAuth } from "../../../lib/auth";
import { ApiError } from "../../../lib/api";
import { useI18n } from "../../../lib/i18n";
import {
  ACTION_ORDER,
  checksSummary,
  reconcile,
  tvChangesApi,
  type ChangeAction,
  type RollbackPoint,
  type StagingConfig,
  type TvChangeDetail,
} from "../../../lib/tvChanges";
import { ApproveDialog, type SwapDialogMode } from "./ApproveDialog";
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
import { ChecksTable } from "./ChecksTable";
import { ConfirmDialog } from "./ConfirmDialog";
import { PreviewPanel } from "./PreviewPanel";
import { ArtDiff } from "./diff/ArtDiff";
import { LineupDiff } from "./diff/LineupDiff";
import { ScheduleDiff } from "./diff/ScheduleDiff";
import { SettingsDiff } from "./diff/SettingsDiff";
import "../../../styles/tvchanges.css";

export function AdminTvChangeDetail() {
  const { id = "" } = useParams();
  return (
    <AdminGate>
      {/* keyed by id: moving to another change starts clean (no data, tab, dialog or
          message of the previous one) */}
      <DetailInner key={id} />
    </AdminGate>
  );
}

/** Actions not built yet: shown, disabled, with the story that brings them. */
const NOT_YET: Partial<Record<ChangeAction, string>> = {};

type Dialog =
  | { kind: "swap"; mode: SwapDialogMode }
  | { kind: "cancel" }
  | { kind: "rollback" }
  | null;

/** channel id → "101 Quran", from everything the change names. */
function channelNames(
  c: TvChangeDetail,
  t: (k: string, v?: Record<string, string | number>) => string
) {
  const out: Record<string, string> = {};
  const put = (id: string, num?: number | null, name?: string | null) => {
    if (!id || out[id]) return;
    if (name) out[id] = num != null ? t("tvchanges.lineup.channel", { n: num, name }) : name;
  };
  const l = c.diff?.lineup;
  for (const x of [...(l?.added ?? []), ...(l?.removed ?? []), ...(l?.changed ?? [])])
    put(x.id, x.number, x.name);
  for (const x of c.diff?.schedules ?? []) put(x.id, x.number, x.name);
  for (const x of c.diff?.art ?? []) put(x.id, x.number, x.name);
  for (const x of c.pending_summary?.channels ?? []) put(x.id, x.number, x.name);
  return out;
}

function DetailInner() {
  const { id = "" } = useParams();
  const { t, n, formatDate } = useI18n();
  const { user } = useAuth();
  const tk = useTk();
  const [change, setChange] = useState<TvChangeDetail | null>(null);
  const [cfg, setCfg] = useState<StagingConfig | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState<ChangeAction | null>(null);
  const [dialog, setDialog] = useState<Dialog>(null);
  const [tab, setTab] = useState("lineup");
  const [point, setPoint] = useState<RollbackPoint | null>(null);
  // a change the server does not know (404) is not polled again
  const [gone, setGone] = useState(false);

  const load = useCallback(async () => {
    try {
      const c = await tvChangesApi.get(id);
      setChange((prev) => reconcile(prev, c));
      setError(null);
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) setGone(true);
      setError(actionError(t, e));
    }
  }, [id, t]);

  useVisiblePoll(load, CHANGES_POLL_MS, !gone);
  // the staging settings (second admin, swap wait): once, with the list call
  useVisiblePoll(async () => {
    try {
      const l = await tvChangesApi.list(["draft"], 1);
      setCfg((prev) => reconcile(prev, l.config));
    } catch {
      /* the dialog falls back to the change's own values */
    }
  }, 60_000);

  // story 33.9 AC7: warm (instant) while the previous slot is up, cold after, none
  const canRollback = !!change?.allowed_actions?.includes("rollback");
  useVisiblePoll(
    async () => {
      try {
        const p = await tvChangesApi.rollbackPoint();
        setPoint((prev) => reconcile(prev, p));
      } catch {
        /* the button stays as it was */
      }
    },
    CHANGES_POLL_MS,
    canRollback
  );

  const names = useMemo(() => (change ? channelNames(change, t) : {}), [change, t]);

  async function run(a: ChangeAction) {
    if (!change) return;
    if (a === "approve" || a === "schedule" || a === "promote") {
      setDialog({ kind: "swap", mode: a });
      return;
    }
    if (a === "cancel") {
      setMsg(null);
      setDialog({ kind: "cancel" });
      return;
    }
    if (a === "rollback") {
      setMsg(null);
      setDialog({ kind: "rollback" });
      return;
    }
    setBusy(a);
    setMsg(null);
    try {
      const out =
        a === "stage" ? await tvChangesApi.stage(change.id) : await tvChangesApi.verify(change.id);
      if (out && typeof out === "object" && "id" in (out as object))
        setChange(out as TvChangeDetail);
      setMsg({ ok: true, text: t(`tvchanges.done.${a}`) });
    } catch (e) {
      setMsg({ ok: false, text: actionError(t, e) });
    } finally {
      setBusy(null);
    }
  }

  async function rollback() {
    if (!change) return;
    setBusy("rollback");
    try {
      const r = await tvChangesApi.rollback();
      setMsg({ ok: true, text: t(`tvchanges.done.rollback.${r.mode}`) });
      setDialog(null);
      void load();
    } catch (e) {
      setMsg({ ok: false, text: actionError(t, e) });
    } finally {
      setBusy(null);
    }
  }

  async function cancel() {
    if (!change) return;
    setBusy("cancel");
    try {
      setChange(await tvChangesApi.cancel(change.id));
      setMsg({ ok: true, text: t("tvchanges.done.cancel") });
      setDialog(null);
    } catch (e) {
      setMsg({ ok: false, text: actionError(t, e) });
    } finally {
      setBusy(null);
    }
  }

  if (error && !change) {
    return (
      <section className="mkt-page mkt-tvx">
        <h1>{t("tvchanges.title")}</h1>
        <p role="alert">{t("tvchanges.loadError", { error })}</p>
        <Link to="/admin/live-tv/changes">{t("tvchanges.back")}</Link>
      </section>
    );
  }
  if (!change) {
    return (
      <section className="mkt-page mkt-tvx">
        <div className="mkt-tvx-skeleton mkt-tvx-skeleton--tall" aria-hidden="true" />
      </section>
    );
  }

  const staged = !!change.release_id;
  const s = checksSummary(change.verify_report);
  const allowed = new Set(change.allowed_actions ?? []);
  const approvedBy = change.approved_by ?? [];
  const shownActions = ACTION_ORDER.filter((a) => allowed.has(a));
  // a failed cancel / roll back, inside the dialog: the page behind it is inert
  const failed =
    msg && !msg.ok ? (
      <p role="alert" className="mkt-tvx-error">
        {msg.text}
      </p>
    ) : null;

  return (
    <section className="mkt-page mkt-tvx" data-testid="change-detail">
      <header className="mkt-page__header mkt-tvx-head">
        <h1>
          <span className="mkt-tvx-num">#{n(change.number)}</span>{" "}
          {change.title || t("tvchanges.untitled")}
        </h1>
        <Link to="/admin/live-tv/changes">{t("tvchanges.back")}</Link>
      </header>

      <dl className="mkt-tvx-facts mkt-tvx-facts--head">
        <dt>{t("tvchanges.col.state")}</dt>
        <dd>
          <StateChip state={change.state} />
          <span className="mkt-tvx-progress" data-testid="progress">
            {progressText(change, t, n, s)}
          </span>
        </dd>
        <dt>{t("tvchanges.col.kinds")}</dt>
        <dd>
          <Kinds kinds={change.kinds} />
        </dd>
        <dt>{t("tvchanges.col.source")}</dt>
        <dd>
          {sourceLabel(tk, change)}
          {typeof change.source?.stage === "string" && (
            <>
              {" · "}
              <Link to={`/queue/${encodeURIComponent(change.source.stage)}`}>
                {t("tvchanges.jobLink")}
              </Link>
            </>
          )}
        </dd>
        <dt>{t("tvchanges.col.author")}</dt>
        <dd>
          {t("tvchanges.byAt", {
            who: userName(change.users, change.created_by, t("tvchanges.system")),
            at: formatDate(change.created_at),
          })}
        </dd>
        <dt>{t("tvchanges.col.checks")}</dt>
        <dd>
          <ChecksSummary report={change.verify_report} />
        </dd>
        <dt>{t("tvchanges.col.swap")}</dt>
        <dd>
          {change.swapped_at ? (
            t("tvchanges.swappedAt", { at: formatDate(change.swapped_at) })
          ) : change.swap_at ? (
            <>
              {t("tvchanges.swapAt")} <Clock at={change.swap_at} />
            </>
          ) : (
            t("tvchanges.swapAsap")
          )}
          {" · "}
          {t(`tvchanges.swapWait.${change.swap_wait ?? cfg?.swap_wait ?? "boundary"}`)}
        </dd>
        <dt>{t("tvchanges.col.approved")}</dt>
        <dd>
          {approvedBy.length ? approvedBy.map((u) => userName(change.users, u, u)).join(", ") : "—"}
        </dd>
      </dl>

      <div className="mkt-tvx-actions" role="toolbar" aria-label={t("tvchanges.actions")}>
        {shownActions.length === 0 && (
          <span className="mkt-tvx-muted">{t("tvchanges.noActions")}</span>
        )}
        {shownActions.map((a) => {
          const story = NOT_YET[a];
          const noPoint = a === "rollback" && point?.mode === "none";
          const btn = (
            <Button
              key={a}
              size="sm"
              variant={
                a === "cancel" || a === "rollback"
                  ? "destructive"
                  : a === "approve"
                    ? "primary"
                    : "secondary"
              }
              disabled={!!story || noPoint || busy !== null}
              aria-disabled={story || noPoint ? true : undefined}
              onClick={() => void run(a)}
              data-testid={`action-${a}`}
            >
              {t(`tvchanges.action.${a}`)}
            </Button>
          );
          const hint =
            a === "rollback" && point ? (
              <span key={`${a}-hint`} className="mkt-tvx-muted" data-testid="rollback-mode">
                {rollbackHint(point, t, n)}
              </span>
            ) : null;
          if (noPoint)
            return (
              <Tooltip key={a} label={rollbackHint(point, t, n)}>
                <span tabIndex={0} className="mkt-tvx-tipwrap" data-testid={`action-${a}-wrap`}>
                  {btn}
                </span>
              </Tooltip>
            );
          if (hint)
            return (
              <span key={a} className="mkt-tvx-tipwrap">
                {btn} {hint}
              </span>
            );
          return story ? (
            <Tooltip key={a} label={t("tvchanges.notYet", { story })}>
              <span tabIndex={0} className="mkt-tvx-tipwrap" data-testid={`action-${a}-wrap`}>
                {btn}
              </span>
            </Tooltip>
          ) : (
            btn
          );
        })}
      </div>
      <div className="mkt-tvx-status" aria-live="polite">
        {/* a read that failed after a good one: the change shown may be stale */}
        {error && (
          <p role="alert" className="mkt-q-status--bad" data-testid="load-error">
            {t("tvchanges.loadError", { error })}
          </p>
        )}
        {msg && (
          <p
            role={msg.ok ? "status" : "alert"}
            className={msg.ok ? "mkt-q-status--ok" : "mkt-q-status--bad"}
          >
            {msg.text}
          </p>
        )}
      </div>

      <Tabs
        label={t("tvchanges.tabs")}
        value={tab}
        onChange={setTab}
        className="mkt-tvx-tabs"
        items={[
          {
            id: "lineup",
            label: t("tvchanges.tab.lineup"),
            content: (
              <LineupDiff
                lineup={change.diff?.lineup}
                pending={change.pending_summary}
                names={names}
              />
            ),
          },
          {
            id: "schedules",
            label: t("tvchanges.tab.schedules"),
            content: <ScheduleDiff changeId={change.id} staged={staged} />,
          },
          {
            id: "settings",
            label: t("tvchanges.tab.settings"),
            content: <SettingsDiff settings={change.diff?.settings} />,
          },
          {
            id: "art",
            label: t("tvchanges.tab.art"),
            content: (
              <ArtDiff
                art={change.diff?.art}
                binary={change.diff?.binary}
                release={change.release}
              />
            ),
          },
          {
            id: "checks",
            label: t("tvchanges.tab.checks"),
            content: <ChecksTable report={change.verify_report} names={names} />,
          },
          {
            id: "preview",
            label: t("tvchanges.tab.preview"),
            content: <PreviewPanel change={change} />,
          },
          {
            id: "history",
            label: t("tvchanges.tab.history"),
            content: <History change={change} />,
          },
        ]}
      />

      {dialog?.kind === "swap" && (
        <ApproveDialog
          mode={dialog.mode}
          change={change}
          cfg={cfg}
          meId={user?.id}
          onClose={() => setDialog(null)}
          onDone={(c) => {
            setChange(c);
            setDialog(null);
            setMsg({ ok: true, text: t(`tvchanges.done.${dialog.mode}`) });
          }}
        />
      )}
      {dialog?.kind === "cancel" && (
        <ConfirmDialog
          title={t("tvchanges.dialog.cancel.title", { n: n(change.number) })}
          confirmLabel={t("tvchanges.dialog.cancel.confirm")}
          busy={busy === "cancel"}
          onConfirm={() => void cancel()}
          onClose={() => setDialog(null)}
        >
          <p>{t("tvchanges.dialog.cancel.intro")}</p>
          {failed}
        </ConfirmDialog>
      )}
      {dialog?.kind === "rollback" && (
        <ConfirmDialog
          title={t("tvchanges.dialog.rollback.title", { n: n(change.number) })}
          confirmLabel={t("tvchanges.dialog.rollback.confirm")}
          busy={busy === "rollback"}
          onConfirm={() => void rollback()}
          onClose={() => setDialog(null)}
        >
          <p>
            {t(
              point?.mode === "cold"
                ? "tvchanges.dialog.rollback.cold"
                : "tvchanges.dialog.rollback.warm"
            )}
          </p>
          {point && <p className="mkt-tvx-muted">{rollbackHint(point, t, n)}</p>}
          {failed}
        </ConfirmDialog>
      )}
    </section>
  );
}

/** "warm (instant) — 12 min left", "cold (~3 min)", or why there is no rollback point. */
function rollbackHint(
  p: RollbackPoint,
  t: (k: string, v?: Record<string, string | number>) => string,
  n: (v: number) => string
): string {
  if (p.mode === "warm") {
    const left = p.until ? Math.max(0, Math.ceil((Date.parse(p.until) - Date.now()) / 60_000)) : 0;
    return p.until
      ? t("tvchanges.rollback.warm", { min: n(left) })
      : t("tvchanges.rollback.warmNow");
  }
  if (p.mode === "cold") return t("tvchanges.rollback.cold");
  return t(`tvchanges.rollback.none.${p.reason ?? "no_rollback_point"}`);
}

function progressText(
  c: TvChangeDetail,
  t: (k: string, v?: Record<string, string | number>) => string,
  n: (v: number) => string,
  s: { total: number; fail: number }
): string {
  switch (c.state) {
    case "draft":
      return c.diff?.stage?.requested_at
        ? t("tvchanges.progress.queued")
        : t("tvchanges.progress.draft");
    case "staged":
      return t("tvchanges.progress.staged");
    case "verifying":
      return t("tvchanges.progress.verifying", { done: n(s.total), fail: n(s.fail) });
    case "ready":
      return t("tvchanges.progress.ready");
    case "approved":
      return c.swap_at ? t("tvchanges.progress.approvedAt") : t("tvchanges.progress.approved");
    case "swapping":
      return t("tvchanges.progress.swapping");
    case "live":
      return t("tvchanges.progress.live");
    case "failed":
      return t("tvchanges.progress.failed", { fail: n(s.fail) });
    default:
      return c.end_reason ? t("tvchanges.progress.ended", { reason: c.end_reason }) : "";
  }
}

function History({ change }: { change: TvChangeDetail }) {
  const { t, formatDate } = useI18n();
  const tk = useTk();
  const audit = change.audit ?? [];
  const who = (id: string | undefined, source: string) =>
    id ? userName(change.users, id, id) : tk(`tvchanges.actor.${source}`, source);
  return (
    <div className="mkt-tvx-diff" data-testid="history">
      <ol className="mkt-tvx-timeline">
        {(change.events ?? []).map((e) => (
          <li key={e.id}>
            <time dateTime={e.at}>{formatDate(e.at)}</time>
            <span className="mkt-tvx-timeline__what">
              {tk(`tvchanges.event.${e.action}`, e.action)}
              {e.from_state && e.to_state && e.from_state !== e.to_state && (
                <span className="mkt-tvx-muted">
                  {" "}
                  ({t(`tvchanges.state.${e.from_state}`)} → {t(`tvchanges.state.${e.to_state}`)})
                </span>
              )}
            </span>
            <span className="mkt-tvx-muted">{who(e.actor_user_id, e.actor_source)}</span>
          </li>
        ))}
      </ol>
      <h3>{t("tvchanges.audit.title")}</h3>
      {audit.length === 0 ? (
        <p className="mkt-tvx-muted">{t("tvchanges.audit.none")}</p>
      ) : (
        <table className="mkt-tvx-kv" data-testid="audit">
          <thead>
            <tr>
              <th scope="col">{t("tvchanges.audit.at")}</th>
              <th scope="col">{t("tvchanges.audit.action")}</th>
              <th scope="col">{t("tvchanges.audit.actor")}</th>
            </tr>
          </thead>
          <tbody>
            {audit.map((a) => (
              <tr key={a.id}>
                <td>{formatDate(a.at)}</td>
                <td>
                  {tk(`tvchanges.event.${a.action}`, a.action)}
                  {typeof a.payload?.from === "string" &&
                    typeof a.payload?.to === "string" &&
                    a.payload.from !== a.payload.to && (
                      <span className="mkt-tvx-muted">
                        {" "}
                        ({a.payload.from ? t(`tvchanges.state.${a.payload.from}`) : "—"} →{" "}
                        {t(`tvchanges.state.${a.payload.to}`)})
                      </span>
                    )}
                </td>
                <td>{who(a.actor_user_id, a.actor_source || "system")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
