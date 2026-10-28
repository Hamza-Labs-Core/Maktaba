// /queue/file-actions (Epic 47): the file-action ledger, one row per file
// operation (a conversion's swap, a merged duplicate, a download, a restore, a
// purge), with filters by library, kind and state, Restore on a row whose file
// is still in the trash, and each library's trash with its next purge.
// Restore only queues work (a file_restore job); the API enforces admin.
import { useCallback, useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { FILE_ACTION_KINDS, FILE_ACTION_STATES, queueApi } from "../../lib/queueApi";
import type { FileAction, FileActionFilter, LibraryTrash } from "../../lib/queueApi";
import { useIsAdmin } from "../../components/queue/useIsAdmin";
import { useFmt } from "../../components/queue/format";
import { useBytes, useDate } from "../../components/queue/offline";
import { useAction } from "../../components/queue/useAction";
import { TitleRow } from "../../help/HelpLink";
import "../../styles/queue.css";

function readFilter(p: URLSearchParams): FileActionFilter {
  return {
    library_id: p.get("library") ?? undefined,
    kind: p.get("kind") ?? undefined,
    state: p.get("state") ?? undefined,
    restorable: p.get("restorable") === "true",
  };
}

/** What the ledger row's check measured, in a line (a failed conversion's why). */
function verifyLine(v: FileAction["verify"]): string | null {
  if (!v || typeof v !== "object") return null;
  const reasons = (v as { reasons?: unknown }).reasons;
  if (Array.isArray(reasons) && reasons.length) return reasons.map(String).join(", ");
  return null;
}

export function FileActions() {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const bytes = useBytes();
  const date = useDate();
  const admin = useIsAdmin();
  const [params, setParams] = useSearchParams();
  const filter = readFilter(params);
  const key = params.toString();
  const [items, setItems] = useState<FileAction[] | null>(null);
  const [trash, setTrash] = useState<LibraryTrash[]>([]);
  const [next, setNext] = useState<string | null>(null);
  const [loadError, setLoadError] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const { busy, error, run } = useAction();

  const load = useCallback(() => {
    setLoadError(false);
    queueApi
      .fileActions(readFilter(new URLSearchParams(key)))
      .then((r) => {
        setItems(Array.isArray(r?.items) ? r.items : []);
        setTrash(Array.isArray(r?.trash) ? r.trash : []);
        setNext(r?.next ?? null);
      })
      .catch(() => setLoadError(true));
  }, [key]);

  useEffect(() => {
    if (admin) load();
  }, [load, admin]);

  const more = () => {
    if (!next) return;
    queueApi
      .fileActions(filter, next)
      .then((r) => {
        setItems((prev) => [...(prev ?? []), ...(r?.items ?? [])]);
        setNext(r?.next ?? null);
      })
      .catch(() => setLoadError(true));
  };

  const setFilter = (k: string, v: string) => {
    const p = new URLSearchParams(params);
    if (v) p.set(k, v);
    else p.delete(k);
    setParams(p, { replace: true });
  };

  const restore = async (a: FileAction) => {
    const r = await run(`restore:${a.id}`, () => queueApi.restoreFileAction(a.id));
    if (r) {
      setNotice(
        t(r.outcome === "inserted" ? "queue.fa.restoreQueued" : "queue.fa.restoreJoined", {
          name: a.video_title ?? a.old_path ?? a.id,
        })
      );
      load();
    }
  };

  if (!admin) {
    return (
      <section className="mkt-page mkt-q">
        <TitleRow>
          <h1>{t("queue.fa.title")}</h1>
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
            <h1>{t("queue.fa.title")}</h1>
          </TitleRow>
        </div>
        <div className="mkt-q-header__actions">
          <Link className="mkt-q-linkbtn" to="/queue">
            {t("queue.detail.back")}
          </Link>
        </div>
      </header>
      <p className="mkt-q-muted">{t("queue.fa.intro")}</p>

      <section className="mkt-q-panel" aria-labelledby="mkt-q-fa-trash-h">
        <h2 className="mkt-q-section-title" id="mkt-q-fa-trash-h">
          {t("queue.fa.trash")}
        </h2>
        {trash.length === 0 ? (
          <p className="mkt-q-empty">{t("queue.fa.trashEmpty")}</p>
        ) : (
          <ul className="mkt-q-fa__trash">
            {trash.map((tr) => (
              <li key={tr.library_id}>
                <button
                  type="button"
                  className="mkt-q-linkbtn"
                  onClick={() => setFilter("library", tr.library_id)}
                >
                  {bidiIsolate(tr.name)}
                </button>{" "}
                {t("queue.fa.trashLine", {
                  n: n(tr.entries),
                  size: bytes(tr.bytes),
                  next: date(tr.next_purge_at),
                })}
                {tr.last_purge_at && (
                  <span className="mkt-q-muted">
                    {" "}
                    · {t("queue.fa.lastPurge", { time: fmt.ago(tr.last_purge_at) })}
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      <form
        className="mkt-q-failures__filters"
        aria-label={t("queue.failures.filters")}
        onSubmit={(e) => e.preventDefault()}
      >
        <label>
          {t("queue.fa.filter.library")}
          <select
            value={filter.library_id ?? ""}
            onChange={(e) => setFilter("library", e.target.value)}
          >
            <option value="">{t("queue.failures.any")}</option>
            {filter.library_id && !trash.some((x) => x.library_id === filter.library_id) && (
              <option value={filter.library_id}>
                {items?.find((i) => i.library_id === filter.library_id)?.library_name ??
                  filter.library_id}
              </option>
            )}
            {trash.map((x) => (
              <option key={x.library_id} value={x.library_id}>
                {x.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          {t("queue.fa.filter.kind")}
          <select value={filter.kind ?? ""} onChange={(e) => setFilter("kind", e.target.value)}>
            <option value="">{t("queue.failures.any")}</option>
            {FILE_ACTION_KINDS.map((k) => (
              <option key={k} value={k}>
                {t(`queue.fa.kind.${k}`)}
              </option>
            ))}
          </select>
        </label>
        <label>
          {t("queue.fa.filter.state")}
          <select value={filter.state ?? ""} onChange={(e) => setFilter("state", e.target.value)}>
            <option value="">{t("queue.failures.any")}</option>
            {FILE_ACTION_STATES.map((s) => (
              <option key={s} value={s}>
                {t(`queue.fa.state.${s}`)}
              </option>
            ))}
          </select>
        </label>
        <label className="mkt-q-check">
          <input
            type="checkbox"
            checked={!!filter.restorable}
            onChange={(e) => setFilter("restorable", e.target.checked ? "true" : "")}
          />
          {t("queue.fa.filter.restorable")}
        </label>
      </form>

      {notice && (
        <p role="status" className="mkt-q-note">
          {notice}
        </p>
      )}
      {(error || loadError) && (
        <p role="alert" className="mkt-q-error">
          {error ?? t("queue.error.load")}
        </p>
      )}
      {items === null && !loadError && <p className="mkt-loading">{t("common.loading")}</p>}
      {items && items.length === 0 && <p className="mkt-q-empty">{t("queue.fa.none")}</p>}
      {items && items.length > 0 && (
        <div className="mkt-q-failures__wrap">
          <table className="mkt-q-failures__table">
            <thead>
              <tr>
                <th scope="col">{t("queue.fa.col.when")}</th>
                <th scope="col">{t("queue.fa.col.what")}</th>
                <th scope="col">{t("queue.fa.col.files")}</th>
                <th scope="col">{t("queue.fa.col.trash")}</th>
                <th scope="col">{t("queue.failures.col.actions")}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((a) => {
                const why = verifyLine(a.verify);
                return (
                  <tr key={a.id} data-state={a.state}>
                    <td>
                      <div>{fmt.ago(a.created_at)}</div>
                      <div className="mkt-q-muted">{bidiIsolate(a.library_name)}</div>
                    </td>
                    <td>
                      <strong>{t(`queue.fa.kind.${a.kind}`)}</strong>{" "}
                      <span className={`mkt-q-chip mkt-q-fa__state--${a.state}`}>
                        {t(`queue.fa.state.${a.state}`)}
                      </span>
                      {a.video_id ? (
                        <div>
                          <Link to={`/queue/items/video/${encodeURIComponent(a.video_id)}`}>
                            {bidiIsolate(a.video_title ?? a.video_id)}
                          </Link>
                        </div>
                      ) : null}
                      {(a.error || why) && (
                        <div className="mkt-q-error" dir="auto">
                          {[a.error, why].filter(Boolean).join(" · ")}
                        </div>
                      )}
                    </td>
                    <td className="mkt-q-fa__paths">
                      {a.old_path && (
                        <div dir="ltr">
                          <span className="mkt-q-muted">{t("queue.fa.from")}</span> {a.old_path}{" "}
                          <span className="mkt-q-muted">({bytes(a.old_size)})</span>
                        </div>
                      )}
                      {a.new_path && (
                        <div dir="ltr">
                          <span className="mkt-q-muted">{t("queue.fa.to")}</span> {a.new_path}{" "}
                          {a.new_size !== null && (
                            <span className="mkt-q-muted">({bytes(a.new_size)})</span>
                          )}
                        </div>
                      )}
                    </td>
                    <td>
                      {a.trash_path ? (
                        a.purged_at ? (
                          t("queue.fa.purgedOn", { date: date(a.purged_at) })
                        ) : (
                          <>
                            <div className="mkt-q-fa__paths" dir="ltr">
                              {a.trash_path}
                            </div>
                            <div className="mkt-q-muted">
                              {t("queue.fa.purgeAfter", { date: date(a.purge_after) })}
                            </div>
                          </>
                        )
                      ) : (
                        <span className="mkt-q-muted">–</span>
                      )}
                    </td>
                    <td>
                      {a.restorable &&
                        (a.restore_queued ? (
                          <span className="mkt-q-muted">{t("queue.fa.restoreIsQueued")}</span>
                        ) : (
                          <Button
                            size="sm"
                            variant="secondary"
                            loading={busy === `restore:${a.id}`}
                            disabled={busy != null && busy !== `restore:${a.id}`}
                            onClick={() => void restore(a)}
                          >
                            {t("queue.fa.restore")}
                            <span className="mkt-visually-hidden">
                              {" "}
                              {a.video_title ?? a.old_path ?? ""}
                            </span>
                          </Button>
                        ))}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          {next && (
            <Button variant="secondary" size="sm" onClick={more}>
              {t("queue.failures.more")}
            </Button>
          )}
        </div>
      )}
    </section>
  );
}
