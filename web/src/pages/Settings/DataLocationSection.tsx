// Settings → Storage & discovery: where Maktaba keeps its data (the bundled
// database, backups, models, caches), how big each folder is and which disk it
// is on, and moving all of it to another disk. The move is checked first (its
// problems block it), confirmed, then the server stops and copies: the page
// waits for it to answer again, then shows how the move went. The old copy is
// kept until the owner deletes it.
import { useCallback, useEffect, useRef, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { Radio } from "@ds/components/Choice/Radio";
import { ApiError } from "../../lib/api";
import { formatBytes } from "../../lib/desktopSetup";
import { useI18n } from "../../lib/i18n";
import { problemMessage } from "../../lib/libraryAdminApi";
import {
  serverSettingsApi,
  type MoveCheck,
  type StorageDisk,
  type StorageDoc,
  type StorageNote,
} from "../../lib/serverSettingsApi";
import { HelpLink } from "../../help/HelpLink";
import { codedText, diskProblem, type Translate } from "../Setup/codedText";

/** How often the page looks whether the server is back from a move. */
export const MOVE_POLL_MS = 3000;

type Phase = "idle" | "confirm" | "moving";

/** A finding in the page's language; its English when the app has no text for it. */
function noteText(t: Translate, n: StorageNote): string {
  return codedText(t, "storage.location.note", n.code, n.params, {}, n.message);
}

/** A folder's name in the page's language (its own name when the app has none). */
function folderName(t: Translate, name: string): string {
  const key = `storage.location.folder.${name}`;
  const text = t(key);
  return text === key ? name : text;
}

function DiskSpace({ d }: { d: StorageDisk }) {
  const { t } = useI18n();
  return (
    <span dir="ltr">
      {t("storage.location.space", {
        free: formatBytes(d.free_bytes),
        total: formatBytes(d.total_bytes),
      })}
    </span>
  );
}

export function DataLocationSection({ pollMs = MOVE_POLL_MS }: { pollMs?: number }) {
  const { t, formatDate } = useI18n();
  const [doc, setDoc] = useState<StorageDoc | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [phase, setPhase] = useState<Phase>("idle");
  const [choosing, setChoosing] = useState(false);
  const [disks, setDisks] = useState<StorageDisk[] | null>(null);
  const [disksError, setDisksError] = useState<string | null>(null);
  const [mount, setMount] = useState("");
  const [target, setTarget] = useState("");
  const [check, setCheck] = useState<MoveCheck | null>(null);
  const [checking, setChecking] = useState(false);
  const [moveError, setMoveError] = useState<string | null>(null);
  const alive = useRef(true);
  const waiting = useRef(false);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const load = useCallback(() => {
    setError(null);
    serverSettingsApi
      .storage()
      .then(setDoc)
      .catch((e) => setError(problemMessage(e, t("common.error"))));
  }, [t]);
  useEffect(load, [load]);

  // the server stops while it copies: wait for it to go away and answer again
  // (or, if it is seen answering all along, for its move to end), then show
  // how the move went
  const waitBack = useCallback(async () => {
    if (waiting.current) return;
    waiting.current = true;
    setPhase("moving");
    let down = false;
    for (let i = 0; alive.current; i++) {
      await new Promise((r) => setTimeout(r, pollMs));
      if (!alive.current) break;
      try {
        await serverSettingsApi.ping();
      } catch {
        down = true;
        continue;
      }
      const d = await serverSettingsApi.storage().catch(() => null);
      if (!alive.current) break;
      if (!d) continue;
      const busy = d.available && (d.move.state === "requested" || d.move.state === "moving");
      if ((down || i >= 4) && !busy) {
        setDoc(d);
        setPhase("idle");
        setChoosing(false);
        setCheck(null);
        break;
      }
    }
    waiting.current = false;
  }, [pollMs]);

  // a move asked for already (the page opened while it waits to start)
  const pending = doc?.available && (doc.move.state === "requested" || doc.move.state === "moving");
  useEffect(() => {
    if (pending) void waitBack();
  }, [pending, waitBack]);

  const choose = () => {
    setChoosing(true);
    setDisks(null);
    setDisksError(null);
    setCheck(null);
    setMoveError(null);
    serverSettingsApi
      .disks()
      .then((r) => setDisks(r.disks))
      .catch((e) => setDisksError(problemMessage(e, t("common.error"))));
  };

  const runCheck = async () => {
    setChecking(true);
    setCheck(null);
    setMoveError(null);
    try {
      setCheck(await serverSettingsApi.checkMove(target.trim()));
    } catch (e) {
      setMoveError(problemMessage(e, t("common.error")));
    } finally {
      setChecking(false);
    }
  };

  const move = async () => {
    setPhase("idle");
    setMoveError(null);
    try {
      await serverSettingsApi.move(target.trim());
    } catch (e) {
      // 422: the same findings as the check (something changed since)
      const body = e instanceof ApiError ? (e.problem as unknown as Partial<MoveCheck>) : null;
      if (e instanceof ApiError && e.status === 422 && Array.isArray(body?.problems)) {
        setCheck({
          ok: false,
          target: target.trim(),
          need_bytes: body.need_bytes ?? 0,
          free_bytes: body.free_bytes ?? 0,
          problems: body.problems,
          warnings: body.warnings ?? [],
        });
      } else if (e instanceof ApiError && e.status === 409) {
        setMoveError(t("storage.location.cannotMove"));
      } else {
        setMoveError(problemMessage(e, t("common.error")));
      }
      return;
    }
    void waitBack();
  };

  if (error) {
    return (
      <section className="mkt-storage__reach" aria-labelledby="mkt-storage-location">
        <h3 id="mkt-storage-location">{t("storage.location.title")}</h3>
        <p className="mkt-error" role="alert">
          {error}
        </p>
        <Button type="button" variant="secondary" onClick={load}>
          {t("common.retry")}
        </Button>
      </section>
    );
  }
  if (!doc) return <p className="mkt-loading">{t("common.loading")}</p>;

  return (
    <section className="mkt-storage__reach" aria-labelledby="mkt-storage-location">
      <h3 id="mkt-storage-location">{t("storage.location.title")}</h3>
      {!doc.available ? (
        <>
          <p>{t("storage.location.unavailable")}</p>
          {doc.reason && <p className="mkt-muted mkt-libadmin__small">{doc.reason}</p>}
        </>
      ) : (
        <>
          <p className="mkt-muted mkt-libadmin__small">
            {t("storage.location.desc")}{" "}
            <HelpLink to={{ topic: "storage", anchor: "data-location" }} />
          </p>
          <dl className="mkt-storage__facts">
            <dt>{t("storage.location.dataDir")}</dt>
            <dd>
              <bdi dir="ltr" className="mkt-libadmin__path">
                {doc.data_dir}
              </bdi>
            </dd>
            <dt>{t("storage.location.total")}</dt>
            <dd dir="ltr">{formatBytes(doc.total_bytes)}</dd>
          </dl>

          <MoveStatus doc={doc} phase={phase} target={target.trim()} formatDate={formatDate} />

          <div className="mkt-acl__scroll">
            <table className="mkt-table" aria-label={t("storage.location.folders")}>
              <thead>
                <tr>
                  <th>{t("storage.location.folder")}</th>
                  <th>{t("storage.location.path")}</th>
                  <th>{t("storage.location.size")}</th>
                  <th>{t("storage.location.disk")}</th>
                </tr>
              </thead>
              <tbody>
                {doc.folders.map((f) => (
                  <tr key={f.name}>
                    <td>{folderName(t, f.name)}</td>
                    <td>
                      <bdi dir="ltr" className="mkt-libadmin__path">
                        {f.path}
                      </bdi>
                    </td>
                    <td dir="ltr">{f.exists ? formatBytes(f.bytes) : "—"}</td>
                    <td>
                      <bdi dir="ltr" className="mkt-libadmin__path">
                        {f.disk}
                      </bdi>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <h4>{t("storage.location.disks")}</h4>
          <ul className="mkt-storage__disks">
            {doc.disks.map((d) => (
              <li key={d.mount}>
                <bdi dir="ltr" className="mkt-libadmin__path">
                  {d.mount}
                </bdi>{" "}
                <DiskSpace d={d} />
              </li>
            ))}
          </ul>

          {phase !== "moving" &&
            (doc.can_move ? (
              !choosing && (
                <Button type="button" variant="secondary" onClick={choose}>
                  {t("storage.location.move")}
                </Button>
              )
            ) : (
              <p className="mkt-muted mkt-libadmin__small">
                {t("storage.location.cannotMoveHere")}
                {doc.reason ? ` ${doc.reason}` : ""}
              </p>
            ))}

          {choosing && doc.can_move && phase !== "moving" && (
            <fieldset className="mkt-storage__move">
              <legend>{t("storage.location.chooseTitle")}</legend>
              {disksError && (
                <p className="mkt-error" role="alert">
                  {disksError}
                </p>
              )}
              {!disks && !disksError && <p role="status">{t("common.loading")}</p>}
              {disks?.map((d) => (
                <div key={d.mount} className="mkt-storage__disk">
                  <Radio
                    name="mkt-storage-disk"
                    checked={mount === d.mount}
                    disabled={!d.suitable || d.current}
                    onChange={() => {
                      setMount(d.mount);
                      setTarget(d.suggested_dir);
                      setCheck(null);
                    }}
                    label={
                      <>
                        <bdi>{d.name || d.mount}</bdi>{" "}
                        <bdi dir="ltr" className="mkt-libadmin__path">
                          ({d.mount})
                        </bdi>{" "}
                        <DiskSpace d={d} />{" "}
                        {d.current && <Badge>{t("storage.location.current")}</Badge>}{" "}
                        <Badge tone="neutral">
                          {t(
                            d.internal ? "storage.location.internal" : "storage.location.external"
                          )}
                        </Badge>
                      </>
                    }
                  />
                  {!d.suitable && d.problem && (
                    <p className="mkt-muted mkt-libadmin__small">{diskProblem(t, d.problem)}</p>
                  )}
                </div>
              ))}
              <Input
                label={t("storage.location.target")}
                description={t("storage.location.targetHelp")}
                value={target}
                dir="ltr"
                spellCheck={false}
                autoComplete="off"
                placeholder="/Volumes/…/Maktaba"
                onChange={(e) => {
                  setTarget(e.target.value);
                  setMount("");
                  setCheck(null);
                }}
              />
              {check && (
                <div role={check.ok ? "status" : "alert"} className="mkt-storage__check">
                  {check.ok && (
                    <p>
                      {t("storage.location.checkOk", {
                        need: formatBytes(check.need_bytes),
                        free: formatBytes(check.free_bytes),
                      })}
                    </p>
                  )}
                  {check.problems.map((n, i) => (
                    <p key={`p-${n.code}-${i}`} className="mkt-storage__warn mkt-error">
                      {noteText(t, n)}
                    </p>
                  ))}
                  {check.warnings.map((n, i) => (
                    <p key={`w-${n.code}-${i}`} className="mkt-storage__warn">
                      {noteText(t, n)}
                    </p>
                  ))}
                </div>
              )}
              {moveError && (
                <p className="mkt-error" role="alert">
                  {moveError}
                </p>
              )}
              <div className="mkt-storage__actions">
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => {
                    setChoosing(false);
                    setCheck(null);
                    setMoveError(null);
                  }}
                >
                  {t("common.cancel")}
                </Button>
                <Button
                  type="button"
                  variant="secondary"
                  loading={checking}
                  disabled={!target.trim() || checking}
                  onClick={() => void runCheck()}
                >
                  {t("storage.location.check")}
                </Button>
                <Button
                  type="button"
                  disabled={!check?.ok || check.target !== target.trim()}
                  onClick={() => setPhase("confirm")}
                >
                  {t("storage.location.moveNow")}
                </Button>
              </div>
            </fieldset>
          )}

          <Modal
            open={phase === "confirm"}
            onClose={() => setPhase("idle")}
            title={t("storage.location.confirmTitle")}
            dismissable={false}
            footer={
              <>
                <Button type="button" variant="ghost" onClick={() => setPhase("idle")}>
                  {t("common.cancel")}
                </Button>
                <Button type="button" onClick={() => void move()}>
                  {t("storage.location.moveNow")}
                </Button>
              </>
            }
          >
            <p>{t("storage.location.confirmBody", { target: target.trim() })}</p>
          </Modal>
        </>
      )}
    </section>
  );
}

/** How the last move went, or that one is under way. */
function MoveStatus({
  doc,
  phase,
  target,
  formatDate,
}: {
  doc: Extract<StorageDoc, { available: true }>;
  phase: Phase;
  /** the folder just chosen, while the doc does not name the move yet */
  target: string;
  formatDate: (v: string) => string;
}) {
  const { t } = useI18n();
  const m = doc.move;
  const to = m.target || target;
  if (phase === "moving" || m.state === "requested" || m.state === "moving") {
    const pct =
      m.total_bytes && m.done_bytes !== undefined
        ? Math.min(100, (m.done_bytes / m.total_bytes) * 100)
        : null;
    return (
      <div className="mkt-storage__banner" role="status">
        <p>{t("storage.location.moving", { target: to })}</p>
        <ProgressBar
          label={t("storage.location.movingLabel")}
          value={pct ?? 0}
          indeterminate={pct === null}
        />
        <p className="mkt-muted mkt-libadmin__small">{t("storage.location.movingNote")}</p>
      </div>
    );
  }
  if (m.state === "failed") {
    return (
      <div className="mkt-storage__banner" role="alert">
        <p>{t("storage.location.failed", { target: to })}</p>
        {m.error && (
          <details>
            <summary>{t("setup.details")}</summary>
            <pre dir="ltr">{m.error}</pre>
          </details>
        )}
      </div>
    );
  }
  if (m.state === "done") {
    return (
      <div className="mkt-storage__banner" role="status">
        <p>
          {m.at
            ? t("storage.location.doneAt", { target: to, at: formatDate(m.at) })
            : t("storage.location.done", { target: to })}
        </p>
        {m.old && m.old.length > 0 && (
          <>
            <p>{t("storage.location.oldCopies")}</p>
            <ul>
              {m.old.map((p) => (
                <li key={p}>
                  <bdi dir="ltr" className="mkt-libadmin__path">
                    {p}
                  </bdi>
                </li>
              ))}
            </ul>
          </>
        )}
      </div>
    );
  }
  return null;
}
