// /queue/failures — every failing item, all stages and classes (Epic 40): what
// failed, why, since when, and the fixes an admin can make for one item, a
// selection or a whole signature group. The filters live in the URL, so a queue
// tile links straight to its own items (?stage=…&status=needs_you).
import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { queueApi } from "../../lib/queueApi";
import type {
  FailureAction,
  FailureActionRequest,
  FailureActionResult,
  FailureFilter,
  FailureItem,
  FailureList,
} from "../../lib/queueApi";
import { useIsAdmin } from "../../components/queue/useIsAdmin";
import { useFmt } from "../../components/queue/format";
import { waitingOnLabel } from "../../components/queue/QueueCard";
import { HelpLink, TitleRow } from "../../help/HelpLink";
import { failureClassHelp, failureStatusHelp } from "../../help/registry";
import "../../styles/queue.css";

const FILTER_KEYS = ["stage", "class", "signature", "library", "path_prefix", "status"] as const;
const STATUSES = ["", "needs_you", "failing", "waiting", "retrying", "ignored", "all"] as const;
const CLASSES = ["", "transient", "dependency", "resource", "item_permanent", "unknown"] as const;

type Dialog =
  | { kind: "fix_path"; item: FailureItem }
  | { kind: "path_map"; scope: Scope }
  | { kind: "permission"; item: FailureItem }
  | null;

/** What an action applies to: some items, or everything the filter matches. */
type Scope = { job_ids: number[] } | { filter: FailureFilter };

function readFilter(params: URLSearchParams): FailureFilter {
  const f: FailureFilter = {};
  for (const k of FILTER_KEYS) {
    const v = params.get(k);
    if (v) (f as Record<string, string>)[k] = v;
  }
  return f;
}

/** The share prefix of a path, as a suggestion for a path-map rule (smb://nas/share/). */
export function sharePrefix(path: string | null): string {
  if (!path) return "";
  const m = /^(smb:\/\/[^/]+\/[^/]+\/)/i.exec(path) ?? /^([a-z]:\/|\/Volumes\/[^/]+\/)/i.exec(path);
  return m ? m[1] : "";
}

export function Failures() {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const admin = useIsAdmin();
  const [params, setParams] = useSearchParams();
  const filter = useMemo(() => readFilter(params), [params]);
  const [data, setData] = useState<FailureList | null>(null);
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [result, setResult] = useState<FailureActionResult | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [dialog, setDialog] = useState<Dialog>(null);
  const [prefixDraft, setPrefixDraft] = useState(filter.path_prefix ?? "");

  const load = useCallback(() => {
    queueApi
      .failureItems(filter)
      .then((d) => {
        if (!d || typeof d !== "object" || !Array.isArray(d.items)) throw new Error("not a list");
        setData(d);
        setError(false);
      })
      .catch(() => setError(true));
  }, [filter]);
  useEffect(() => {
    setSelected(new Set());
    load();
  }, [load]);
  useEffect(() => setPrefixDraft(filter.path_prefix ?? ""), [filter.path_prefix]);

  const more = () => {
    if (!data?.next) return;
    queueApi
      .failureItems(filter, data.next)
      .then((d) => setData((prev) => (prev ? { ...d, items: [...prev.items, ...d.items] } : d)))
      .catch(() => setError(true));
  };

  const setFilter = (k: (typeof FILTER_KEYS)[number], v: string) => {
    const next = new URLSearchParams(params);
    if (v) next.set(k, v);
    else next.delete(k);
    setParams(next, { replace: true });
  };

  const run = async (req: FailureActionRequest) => {
    setBusy(true);
    setActionError(null);
    try {
      const r = await queueApi.failureAction(req);
      setResult(r);
      setDialog(null);
      setSelected(new Set());
      load();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const act = (action: FailureAction, scope: Scope) => {
    if (action === "remove" && !window.confirm(t("queue.failures.confirmRemove"))) return;
    void run({ action, ...scope });
  };

  const items = data?.items ?? [];
  const facets = data?.facets;
  const total = matchingCount(facets?.status, filter.status);
  const allOnPage = items.length > 0 && items.every((i) => selected.has(i.job_id));
  const selectionScope: Scope = { job_ids: [...selected] };
  const groupScope: Scope = { filter };

  const toggle = (id: number) =>
    setSelected((s) => {
      const next = new Set(s);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const bulk = (scope: Scope, label: string) => (
    <div className="mkt-q-failures__bulk" role="group" aria-label={label}>
      <strong>{label}</strong>
      <Button size="sm" variant="secondary" disabled={busy} onClick={() => act("retry", scope)}>
        {t("queue.failures.action.retry")}
      </Button>
      {filter.status === "ignored" ? (
        <Button
          size="sm"
          variant="secondary"
          disabled={busy}
          onClick={() => act("unignore", scope)}
        >
          {t("queue.failures.action.unignore")}
        </Button>
      ) : (
        <Button size="sm" variant="secondary" disabled={busy} onClick={() => act("ignore", scope)}>
          {t("queue.failures.action.ignore")}
        </Button>
      )}
      <Button
        size="sm"
        variant="secondary"
        disabled={busy}
        onClick={() => act("check_access", scope)}
      >
        {t("queue.failures.action.check_access")}
      </Button>
      <Button
        size="sm"
        variant="secondary"
        disabled={busy}
        onClick={() => act("redownload", scope)}
      >
        {t("queue.failures.action.redownload")}
      </Button>
      <Button size="sm" variant="secondary" disabled={busy} onClick={() => act("rescan", scope)}>
        {t("queue.failures.action.rescan")}
      </Button>
      <Button
        size="sm"
        variant="secondary"
        disabled={busy}
        onClick={() => setDialog({ kind: "path_map", scope })}
      >
        {t("queue.failures.action.path_map")}
      </Button>
      <Button size="sm" variant="destructive" disabled={busy} onClick={() => act("remove", scope)}>
        {t("queue.failures.action.remove")}
      </Button>
    </div>
  );

  if (!admin) {
    return (
      <section className="mkt-page mkt-q">
        <TitleRow>
          <h1>{t("queue.failures.title")}</h1>
        </TitleRow>
        <p className="mkt-q-empty">{t("queue.failures.adminOnly")}</p>
      </section>
    );
  }

  return (
    <section className="mkt-page mkt-q">
      <header className="mkt-q-header">
        <div className="mkt-q-header__title">
          <TitleRow>
            <h1>{t("queue.failures.title")}</h1>
          </TitleRow>
        </div>
        <div className="mkt-q-header__actions">
          <Link className="mkt-q-linkbtn" to="/queue">
            {t("queue.detail.back")}
          </Link>
        </div>
      </header>
      <p className="mkt-q-muted">{t("queue.failures.intro")}</p>

      <form
        className="mkt-q-failures__filters"
        aria-label={t("queue.failures.filters")}
        onSubmit={(e) => {
          e.preventDefault();
          setFilter("path_prefix", prefixDraft.trim());
        }}
      >
        <label>
          {t("queue.failures.filter.status")}
          <select value={filter.status ?? ""} onChange={(e) => setFilter("status", e.target.value)}>
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {t(`queue.failures.status.${s || "open"}`)}
                {s && facets?.status.find((f) => f.value === s)
                  ? ` (${n(facets.status.find((f) => f.value === s)!.count)})`
                  : ""}
              </option>
            ))}
          </select>
        </label>
        <label>
          {t("queue.failures.filter.stage")}
          <select value={filter.stage ?? ""} onChange={(e) => setFilter("stage", e.target.value)}>
            <option value="">{t("queue.failures.any")}</option>
            {filter.stage && !facets?.stage.some((f) => f.value === filter.stage) && (
              <option value={filter.stage}>{filter.stage}</option>
            )}
            {facets?.stage.map((f) => (
              <option key={f.value} value={f.value}>
                {f.value} ({n(f.count)})
              </option>
            ))}
          </select>
        </label>
        <label>
          {t("queue.failures.filter.class")}
          <select value={filter.class ?? ""} onChange={(e) => setFilter("class", e.target.value)}>
            {CLASSES.map((c) => (
              <option key={c} value={c}>
                {t(`queue.failures.class.${c || "any"}`)}
              </option>
            ))}
          </select>
        </label>
        <label>
          {t("queue.failures.filter.signature")}
          <select
            value={filter.signature ?? ""}
            onChange={(e) => setFilter("signature", e.target.value)}
          >
            <option value="">{t("queue.failures.any")}</option>
            {filter.signature && !facets?.signature.some((f) => f.value === filter.signature) && (
              <option value={filter.signature}>{filter.signature}</option>
            )}
            {facets?.signature.map((f) => (
              <option key={`${f.label}:${f.value}`} value={f.value}>
                {f.value || t("queue.failures.noSignature")} · {f.label} ({n(f.count)})
              </option>
            ))}
          </select>
        </label>
        <label>
          {t("queue.failures.filter.library")}
          <select
            value={filter.library ?? ""}
            onChange={(e) => setFilter("library", e.target.value)}
          >
            <option value="">{t("queue.failures.any")}</option>
            {facets?.library
              .filter((f) => f.value)
              .map((f) => (
                <option key={f.value} value={f.value}>
                  {f.label || f.value} ({n(f.count)})
                </option>
              ))}
          </select>
        </label>
        <label>
          {t("queue.failures.filter.path")}
          <input
            type="search"
            dir="ltr"
            value={prefixDraft}
            placeholder="smb://nas/share/folder/"
            onChange={(e) => setPrefixDraft(e.target.value)}
            onBlur={() => setFilter("path_prefix", prefixDraft.trim())}
          />
        </label>
      </form>

      {error && (
        <p role="alert" className="mkt-q-banner">
          {t("queue.failures.error")}{" "}
          <Button variant="secondary" size="sm" onClick={load}>
            {t("queue.error.retry")}
          </Button>
        </p>
      )}
      {actionError && (
        <p role="alert" className="mkt-q-banner">
          {t("queue.failures.actionError", { error: actionError })}
        </p>
      )}
      {result && (
        <p role="status" className="mkt-q-muted">
          {t("queue.failures.done", {
            action: t(`queue.failures.action.${result.action}`),
            affected: n(result.affected),
            matched: n(result.matched),
            skipped: n(result.skipped),
          })}
        </p>
      )}

      {data &&
        total !== undefined &&
        total > 0 &&
        (filter.signature ||
          filter.stage ||
          filter.path_prefix ||
          filter.class ||
          filter.library) &&
        bulk(groupScope, t("queue.failures.allMatching", { n: n(total) }))}
      {selected.size > 0 &&
        bulk(selectionScope, t("queue.failures.selected", { n: n(selected.size) }))}

      {dialog?.kind === "path_map" && (
        <PathMapDialog
          busy={busy}
          suggestion={sharePrefix(
            items.find((i) => selected.has(i.job_id))?.path ?? items[0]?.path ?? null
          )}
          onCancel={() => setDialog(null)}
          onSave={(from, to) =>
            void run({ action: "path_map", path_map: { from, to }, ...dialog.scope })
          }
        />
      )}
      {dialog?.kind === "fix_path" && (
        <FixPathDialog
          busy={busy}
          item={dialog.item}
          onCancel={() => setDialog(null)}
          onSave={(path) => void run({ action: "fix_path", job_ids: [dialog.item.job_id], path })}
        />
      )}
      {dialog?.kind === "permission" && (
        <div
          className="mkt-q-failures__dialog"
          role="dialog"
          aria-label={t("queue.failures.permission.title")}
        >
          <strong>{t("queue.failures.permission.title")}</strong>
          <p dir="auto">{dialog.item.message}</p>
          {dialog.item.access_check && (
            <p>
              {dialog.item.access_check.ok
                ? t("queue.failures.check.ok", {
                    host: dialog.item.access_check.host ?? "",
                    time: fmt.ago(dialog.item.access_check.checked_at),
                  })
                : t("queue.failures.check.failed", {
                    host: dialog.item.access_check.host ?? "",
                    time: fmt.ago(dialog.item.access_check.checked_at),
                    error: dialog.item.access_check.error ?? "",
                  })}
            </p>
          )}
          <p className="mkt-q-muted">{t("queue.failures.permission.howto")}</p>
          <div className="mkt-q-failures__actions">
            <Button
              size="sm"
              disabled={busy}
              onClick={() => act("check_access", { job_ids: [dialog.item.job_id] })}
            >
              {t("queue.failures.action.check_access")}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setDialog(null)}>
              {t("queue.failures.close")}
            </Button>
          </div>
        </div>
      )}

      {data === null && !error && <p className="mkt-loading">{t("common.loading")}</p>}
      {data && items.length === 0 && <p className="mkt-q-empty">{t("queue.failures.none")}</p>}
      {items.length > 0 && (
        <div className="mkt-q-failures__wrap">
          <table className="mkt-q-failures__table">
            <thead>
              <tr>
                <th scope="col">
                  <input
                    type="checkbox"
                    aria-label={t("queue.failures.selectPage")}
                    checked={allOnPage}
                    onChange={() =>
                      setSelected(allOnPage ? new Set() : new Set(items.map((i) => i.job_id)))
                    }
                  />
                </th>
                <th scope="col">{t("queue.failures.col.item")}</th>
                <th scope="col">{t("queue.failures.col.stage")}</th>
                <th scope="col">{t("queue.failures.col.error")}</th>
                <th scope="col">{t("queue.failures.col.attempts")}</th>
                <th scope="col">{t("queue.failures.col.when")}</th>
                <th scope="col">{t("queue.failures.col.actions")}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((it) => (
                <tr key={it.job_id} data-status={it.status}>
                  <td>
                    <input
                      type="checkbox"
                      aria-label={t("queue.failures.select", { label: it.label })}
                      checked={selected.has(it.job_id)}
                      onChange={() => toggle(it.job_id)}
                    />
                  </td>
                  <td>
                    <Link
                      to={`/queue/items/${encodeURIComponent(it.subject_kind)}/${encodeURIComponent(it.subject_id)}`}
                    >
                      <span dir="auto">{bidiIsolate(it.label)}</span>
                    </Link>
                    {it.path && (
                      <div className="mkt-q-failures__path mkt-q-muted" dir="ltr">
                        {it.path}
                      </div>
                    )}
                    {it.library_name && (
                      <div className="mkt-q-muted">{bidiIsolate(it.library_name)}</div>
                    )}
                  </td>
                  <td>{it.stage}</td>
                  <td>
                    <strong>{reasonText(it, t)}</strong>
                    <div dir="auto">{it.message}</div>
                    <div className="mkt-q-muted">
                      {t(`queue.failures.status.${it.status}`)}
                      {it.class ? ` · ${it.class}` : ""}{" "}
                      <HelpLink
                        to={it.class ? failureClassHelp(it.class) : failureStatusHelp(it.status)}
                        label={t("help.causeHelpNamed", {
                          cause: it.class ?? t(`queue.failures.status.${it.status}`),
                        })}
                      />
                      {it.signature ? (
                        <>
                          {" · "}
                          <button
                            type="button"
                            className="mkt-q-linkbtn"
                            onClick={() => setFilter("signature", it.signature!)}
                          >
                            {it.signature}
                          </button>
                        </>
                      ) : null}
                    </div>
                  </td>
                  <td>
                    {n(it.attempts)} / {n(it.max_attempts)}
                  </td>
                  <td>
                    <div>{t("queue.failures.first", { time: fmt.ago(it.first_failed_at) })}</div>
                    <div className="mkt-q-muted">
                      {t("queue.failures.last", { time: fmt.ago(it.last_failed_at) })}
                    </div>
                  </td>
                  <td>
                    <div className="mkt-q-failures__actions">
                      <Button
                        size="sm"
                        variant="secondary"
                        disabled={busy}
                        onClick={() => act("retry", { job_ids: [it.job_id] })}
                      >
                        {t("queue.failures.action.retry")}
                      </Button>
                      {it.subject_kind === "video" && (
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={busy}
                          onClick={() => setDialog({ kind: "fix_path", item: it })}
                        >
                          {t("queue.failures.action.fix_path")}
                        </Button>
                      )}
                      {it.subject_kind === "video" && (
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={busy}
                          onClick={() => setDialog({ kind: "permission", item: it })}
                        >
                          {t("queue.failures.action.permission")}
                        </Button>
                      )}
                      {it.youtube_id && (
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={busy}
                          onClick={() => act("redownload", { job_ids: [it.job_id] })}
                        >
                          {t("queue.failures.action.redownload")}
                        </Button>
                      )}
                      {it.status === "ignored" ? (
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={busy}
                          onClick={() => act("unignore", { job_ids: [it.job_id] })}
                        >
                          {t("queue.failures.action.unignore")}
                        </Button>
                      ) : (
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={busy}
                          onClick={() => act("ignore", { job_ids: [it.job_id] })}
                        >
                          {t("queue.failures.action.ignore")}
                        </Button>
                      )}
                      {it.subject_kind === "video" && (
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={busy}
                          onClick={() => act("remove", { job_ids: [it.job_id] })}
                        >
                          {t("queue.failures.action.remove")}
                        </Button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {data?.next && (
            <Button variant="secondary" size="sm" onClick={more}>
              {t("queue.failures.more")}
            </Button>
          )}
        </div>
      )}
    </section>
  );
}

type T = (key: string, vars?: Record<string, string | number>) => string;

/** How many items the filter matches, from the status facet. */
export function matchingCount(
  statuses: { value: string; count: number }[] | undefined,
  status: FailureFilter["status"]
): number | undefined {
  if (!statuses) return undefined;
  return statuses
    .filter((s) => (!status ? s.value !== "ignored" : status === "all" || s.value === status))
    .reduce((a, s) => a + s.count, 0);
}

/** Why an item is here, in words. */
export function reasonText(it: FailureItem, t: T): string {
  if (it.reason.startsWith("waiting:")) {
    return t("queue.failures.reason.waiting", { on: waitingOnLabel(it.reason.slice(8), t) });
  }
  return t(`queue.failures.reason.${it.reason}`);
}

function PathMapDialog({
  busy,
  suggestion,
  onCancel,
  onSave,
}: {
  busy: boolean;
  suggestion: string;
  onCancel: () => void;
  onSave: (from: string, to: string) => void;
}) {
  const { t } = useI18n();
  const [from, setFrom] = useState(suggestion);
  const [to, setTo] = useState("");
  return (
    <form
      className="mkt-q-failures__dialog"
      aria-label={t("queue.failures.pathMap.title")}
      onSubmit={(e) => {
        e.preventDefault();
        if (from.trim() && to.trim()) onSave(from.trim(), to.trim());
      }}
    >
      <strong>{t("queue.failures.pathMap.title")}</strong>
      <p className="mkt-q-muted">{t("queue.failures.pathMap.help")}</p>
      <label>
        {t("queue.failures.pathMap.from")}
        <input dir="ltr" value={from} onChange={(e) => setFrom(e.target.value)} />
      </label>
      <label>
        {t("queue.failures.pathMap.to")}
        <input
          dir="ltr"
          value={to}
          placeholder="/Volumes/share/"
          onChange={(e) => setTo(e.target.value)}
        />
      </label>
      <div className="mkt-q-failures__actions">
        <Button size="sm" type="submit" disabled={busy || !from.trim() || !to.trim()}>
          {t("queue.failures.pathMap.save")}
        </Button>
        <Button size="sm" variant="ghost" type="button" onClick={onCancel}>
          {t("queue.failures.close")}
        </Button>
      </div>
    </form>
  );
}

function FixPathDialog({
  busy,
  item,
  onCancel,
  onSave,
}: {
  busy: boolean;
  item: FailureItem;
  onCancel: () => void;
  onSave: (path: string) => void;
}) {
  const { t } = useI18n();
  const [path, setPath] = useState(item.path ?? "");
  return (
    <form
      className="mkt-q-failures__dialog"
      aria-label={t("queue.failures.fixPath.title", { label: item.label })}
      onSubmit={(e) => {
        e.preventDefault();
        if (path.trim()) onSave(path.trim());
      }}
    >
      <strong>{t("queue.failures.fixPath.title", { label: item.label })}</strong>
      <p className="mkt-q-muted">{t("queue.failures.fixPath.help")}</p>
      <label>
        {t("queue.failures.fixPath.path")}
        <input dir="ltr" value={path} onChange={(e) => setPath(e.target.value)} />
      </label>
      <div className="mkt-q-failures__actions">
        <Button
          size="sm"
          type="submit"
          disabled={busy || !path.trim() || path.trim() === item.path}
        >
          {t("queue.failures.fixPath.save")}
        </Button>
        <Button size="sm" variant="ghost" type="button" onClick={onCancel}>
          {t("queue.failures.close")}
        </Button>
      </div>
    </form>
  );
}
