// Epic 45 (story 45.5): Settings → Storage & discovery (admins). The server's
// [media] path_map and roots, LAN discovery (mdns), its listen addresses and
// public URLs, as saved in server.toml. Save checks every value on the server
// first (422 names the field); a change applies at the next restart, which the
// admin starts here (when the server runs under the launcher) and watches: the
// page waits for the server to answer again, then reloads. The reachability
// table says which library folders this machine reaches through the saved map.
// Below it, where Maktaba keeps its own data and moving it to another disk
// (DataLocationSection), and the backups of the library database
// (BackupsSection), whose saved changes wait for the same restart.
import { useCallback, useEffect, useRef, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { fieldErrors, problemMessage } from "../../lib/libraryAdminApi";
import { reloadPage } from "../../lib/navigation";
import {
  ADDRESS_KEYS,
  URL_KEYS,
  serverSettingsApi,
  type PathMapPair,
  type ReachabilityRow,
  type ServerSettings,
  type ServerSettingsDoc,
} from "../../lib/serverSettingsApi";
import { HelpLink } from "../../help/HelpLink";
import { BackupsSection } from "./BackupsSection";
import { DataLocationSection } from "./DataLocationSection";
import "../../styles/libraryAdmin.css";
import { LinesPlaceholder, Loading } from "../../components/Placeholders";

/** How often the page looks whether the restarted server answers, and for how long. */
export const RESTART_POLL_MS = 2000;
export const RESTART_POLL_MAX = 90; // 3 minutes

type ServerKey = keyof ServerSettings["server"];
type Phase = "idle" | "confirm" | "restarting" | "slow";

const blankPair = (): PathMapPair => ({ db: "", local: "" });
const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

/** The rows to send (fully blank ones dropped) and each one's row index. */
function kept<T>(rows: T[], blank: (r: T) => boolean): { list: T[]; rowOf: number[] } {
  const list: T[] = [];
  const rowOf: number[] = [];
  rows.forEach((r, i) => {
    if (blank(r)) return;
    list.push(r);
    rowOf.push(i);
  });
  return { list, rowOf };
}

export function StorageSettings({
  pollMs = RESTART_POLL_MS,
  onReload = reloadPage,
}: {
  /** for tests: the restart poll's interval */
  pollMs?: number;
  /** for tests: what "reload" does */
  onReload?: () => void;
} = {}) {
  const { user } = useAuth();
  const { t } = useI18n();
  if (!user?.is_admin) {
    return (
      <ErrorState
        kind="permission"
        title={t("common.forbidden.title")}
        description={t("common.forbidden.desc")}
      />
    );
  }
  return <StorageInner pollMs={pollMs} onReload={onReload} />;
}

function StorageInner({ pollMs, onReload }: { pollMs: number; onReload: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const [doc, setDoc] = useState<ServerSettingsDoc | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [form, setForm] = useState<ServerSettings | null>(null);
  const [saving, setSaving] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [phase, setPhase] = useState<Phase>("idle");
  const [restartError, setRestartError] = useState<string | null>(null);
  // saved backup settings wait for the same restart
  const [backupsRestart, setBackupsRestart] = useState(false);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const show = useCallback((d: ServerSettingsDoc) => {
    setDoc(d);
    if (d.available) {
      const s = structuredClone(d.saved);
      if (s.media.path_map.length === 0) s.media.path_map.push(blankPair());
      if (s.media.roots.length === 0) s.media.roots.push("");
      setForm(s);
    }
  }, []);

  const load = useCallback(() => {
    setError(null);
    serverSettingsApi
      .get()
      .then(show)
      .catch((e) => setError(problemMessage(e, t("common.error"))));
  }, [show, t]);

  useEffect(load, [load]);

  if (error) {
    return (
      <ErrorState
        kind="server"
        title={t("common.error")}
        description={error}
        action={
          <Button type="button" variant="secondary" onClick={load}>
            {t("common.retry")}
          </Button>
        }
      />
    );
  }
  // live server state (a move, a restart, a backup): read each time, its
  // placeholder only after 0.4 s
  if (!doc) {
    return (
      <Loading loading>
        <LinesPlaceholder lines={6} />
      </Loading>
    );
  }
  if (!doc.available || !form) {
    return (
      <section className="mkt-storage" aria-labelledby="mkt-storage-title">
        <h2 id="mkt-storage-title">{t("storage.title")}</h2>
        <p>{t("storage.unavailable")}</p>
        {!doc.available && doc.reason && (
          <p className="mkt-muted mkt-libadmin__small">{doc.reason}</p>
        )}
        <HelpLink to={{ topic: "storage", anchor: "not-available" }} />
      </section>
    );
  }

  const saved = doc.saved;
  const pairs = kept(form.media.path_map, (p) => p.db.trim() === "" && p.local.trim() === "");
  const roots = kept(form.media.roots, (r) => r.trim() === "");
  const pathMap = pairs.list.map((p) => ({ db: p.db.trim(), local: p.local.trim() }));
  const rootList = roots.list.map((r) => r.trim());

  const serverChanges: Partial<ServerSettings["server"]> = {};
  for (const k of Object.keys(saved.server) as ServerKey[]) {
    const v =
      typeof form.server[k] === "string" ? (form.server[k] as string).trim() : form.server[k];
    if (v !== saved.server[k]) (serverChanges as Record<string, unknown>)[k] = v;
  }
  const mediaChanges: Partial<ServerSettings["media"]> = {};
  if (!same(pathMap, saved.media.path_map)) mediaChanges.path_map = pathMap;
  if (!same(rootList, saved.media.roots)) mediaChanges.roots = rootList;
  const dirty = Object.keys(serverChanges).length > 0 || Object.keys(mediaChanges).length > 0;
  const addressChanged = [...ADDRESS_KEYS, ...URL_KEYS].some((k) => k in serverChanges);

  function update(f: (s: ServerSettings) => void) {
    setForm((prev) => {
      if (!prev) return prev;
      const next = structuredClone(prev);
      f(next);
      return next;
    });
  }

  async function save() {
    setSaving(true);
    setErrors({});
    setFormError(null);
    try {
      const body: Parameters<typeof serverSettingsApi.save>[0] = {};
      if (Object.keys(mediaChanges).length > 0) body.media = mediaChanges;
      if (Object.keys(serverChanges).length > 0) body.server = serverChanges;
      const d = await serverSettingsApi.save(body);
      show(d);
      toast.show({ tone: "success", message: t("storage.saved") });
    } catch (e) {
      // each field error beside its field: media.path_map[i] (a pair as a whole:
      // shown on its local side), media.path_map[i].db|.local, media.roots[i],
      // server.<key>; the indexes are of the list sent
      const next: Record<string, string> = {};
      const loose: string[] = [];
      for (const fe of fieldErrors(e)) {
        const pm = /^media\.path_map\[(\d+)\](?:\.(db|local))?$/.exec(fe.field);
        const rt = /^media\.roots\[(\d+)\]$/.exec(fe.field);
        const sv = /^server\.(\w+)$/.exec(fe.field);
        if (pm && pairs.rowOf[Number(pm[1])] !== undefined) {
          next[`path_map.${pairs.rowOf[Number(pm[1])]}.${pm[2] ?? "local"}`] = fe.message;
        } else if (rt && roots.rowOf[Number(rt[1])] !== undefined) {
          next[`roots.${roots.rowOf[Number(rt[1])]}`] = fe.message;
        } else if (sv && sv[1] in saved.server) {
          next[sv[1]] = fe.message;
        } else {
          loose.push(fe.field ? `${fe.field}: ${fe.message}` : fe.message);
        }
      }
      setErrors(next);
      if (loose.length > 0) setFormError(loose.join(" "));
      else if (Object.keys(next).length === 0) setFormError(problemMessage(e, t("common.error")));
    } finally {
      setSaving(false);
    }
  }

  async function restart() {
    setRestartError(null);
    try {
      await serverSettingsApi.restart();
    } catch (e) {
      setPhase("idle");
      setRestartError(
        e instanceof ApiError && e.status === 409
          ? t("storage.restart.cannot")
          : problemMessage(e, t("common.error"))
      );
      return;
    }
    setPhase("restarting");
    // reload once the server answers after it went away; one that comes back
    // too fast to be seen gone is known by its settings: no restart needed
    let down = false;
    for (let i = 0; i < RESTART_POLL_MAX && alive.current; i++) {
      await new Promise((r) => setTimeout(r, pollMs));
      if (!alive.current) return;
      try {
        await serverSettingsApi.ping();
      } catch {
        down = true;
        continue;
      }
      if (down) return onReload();
      if (i >= 4) {
        const d = await serverSettingsApi.get().catch(() => null);
        const b = await serverSettingsApi.backups().catch(() => null);
        if (d?.available && !d.restart_needed && !(b?.available && b.restart_needed)) {
          return onReload();
        }
      }
    }
    if (alive.current) setPhase("slow");
  }

  const s = form.server;
  return (
    <section className="mkt-storage" aria-labelledby="mkt-storage-title">
      <h2 id="mkt-storage-title">{t("storage.title")}</h2>
      <p className="mkt-muted">{t("storage.desc")}</p>
      {doc.file && (
        <p className="mkt-muted mkt-libadmin__small">
          {t("storage.file")}{" "}
          <bdi dir="ltr" className="mkt-libadmin__path">
            {doc.file}
          </bdi>
        </p>
      )}

      {(doc.restart_needed || backupsRestart || phase !== "idle" || restartError) && (
        <div className="mkt-storage__banner" role="status">
          {phase === "restarting" ? (
            <>
              <p>{t("storage.restart.waiting")}</p>
              <ProgressBar indeterminate label={t("storage.restart.waiting")} />
            </>
          ) : phase === "slow" ? (
            <>
              <p>{t("storage.restart.slow")}</p>
              <Button type="button" variant="secondary" onClick={onReload}>
                {t("common.reloadPage")}
              </Button>
            </>
          ) : (
            <>
              <p>
                <strong>{t("storage.restart.needed")}</strong>
              </p>
              {doc.can_restart ? (
                <Button type="button" onClick={() => setPhase("confirm")}>
                  {t("storage.restart.button")}
                </Button>
              ) : (
                <p>{t("storage.restart.manual")}</p>
              )}
              {restartError && (
                <p className="mkt-error" role="alert">
                  {restartError}
                </p>
              )}
            </>
          )}
        </div>
      )}

      <form
        className="mkt-form mkt-storage__form"
        onSubmit={(e) => {
          e.preventDefault();
          if (dirty) void save();
        }}
      >
        {formError && (
          <p className="mkt-error" role="alert">
            {formError}
          </p>
        )}

        <fieldset>
          <legend>{t("storage.pathMap.title")}</legend>
          <p className="mkt-muted mkt-libadmin__small">{t("storage.pathMap.help")}</p>
          {form.media.path_map.map((p, i) => (
            <div key={i} className="mkt-storage__pair">
              <Input
                label={t("storage.pathMap.db", { n: i + 1 })}
                value={p.db}
                dir="ltr"
                spellCheck={false}
                autoComplete="off"
                placeholder="/Volumes/…"
                error={errors[`path_map.${i}.db`]}
                onChange={(e) => {
                  const v = e.target.value;
                  update((x) => {
                    x.media.path_map[i].db = v;
                  });
                }}
              />
              <Input
                label={t("storage.pathMap.local", { n: i + 1 })}
                value={p.local}
                dir="ltr"
                spellCheck={false}
                autoComplete="off"
                placeholder="/Users/…"
                error={errors[`path_map.${i}.local`]}
                onChange={(e) => {
                  const v = e.target.value;
                  update((x) => {
                    x.media.path_map[i].local = v;
                  });
                }}
              />
              <Button
                type="button"
                size="sm"
                variant="ghost"
                aria-label={t("storage.pathMap.remove", { n: i + 1 })}
                onClick={() =>
                  update((x) => {
                    x.media.path_map.splice(i, 1);
                    if (x.media.path_map.length === 0) x.media.path_map.push(blankPair());
                  })
                }
              >
                {t("libraryAdmin.form.remove")}
              </Button>
            </div>
          ))}
          <Button
            type="button"
            size="sm"
            variant="secondary"
            onClick={() => update((x) => void x.media.path_map.push(blankPair()))}
          >
            {t("storage.pathMap.add")}
          </Button>
        </fieldset>

        <fieldset>
          <legend>{t("storage.roots.title")}</legend>
          <p className="mkt-muted mkt-libadmin__small">{t("storage.roots.help")}</p>
          {form.media.roots.map((r, i) => (
            <div key={i} className="mkt-storage__pair">
              <Input
                label={t("storage.roots.folder", { n: i + 1 })}
                value={r}
                dir="ltr"
                spellCheck={false}
                autoComplete="off"
                error={errors[`roots.${i}`]}
                onChange={(e) => {
                  const v = e.target.value;
                  update((x) => {
                    x.media.roots[i] = v;
                  });
                }}
              />
              <Button
                type="button"
                size="sm"
                variant="ghost"
                aria-label={t("storage.roots.remove", { n: i + 1 })}
                onClick={() =>
                  update((x) => {
                    x.media.roots.splice(i, 1);
                    if (x.media.roots.length === 0) x.media.roots.push("");
                  })
                }
              >
                {t("libraryAdmin.form.remove")}
              </Button>
            </div>
          ))}
          <Button
            type="button"
            size="sm"
            variant="secondary"
            onClick={() => update((x) => void x.media.roots.push(""))}
          >
            {t("storage.roots.add")}
          </Button>
        </fieldset>

        <fieldset>
          <legend>{t("storage.discovery.title")}</legend>
          <Toggle
            label={t("storage.discovery.mdns")}
            checked={s.mdns}
            onChange={(e) => {
              const v = e.target.checked;
              update((x) => {
                x.server.mdns = v;
              });
            }}
          />
          <p className="mkt-muted mkt-libadmin__small">{t("storage.discovery.help")}</p>
        </fieldset>

        <fieldset>
          <legend>{t("storage.addresses.title")}</legend>
          <p className="mkt-muted mkt-libadmin__small">{t("storage.addresses.help")}</p>
          {[...ADDRESS_KEYS, ...URL_KEYS].map((k) => (
            <Input
              key={k}
              label={t(`storage.field.${k}`)}
              description={t(`storage.field.${k}.help`)}
              value={s[k]}
              dir="ltr"
              spellCheck={false}
              autoComplete="off"
              error={errors[k]}
              onChange={(e) => {
                const v = e.target.value;
                update((x) => {
                  x.server[k] = v;
                });
              }}
            />
          ))}
          {addressChanged && (
            <p className="mkt-storage__warn" role="note">
              {t("storage.addresses.warn")}
            </p>
          )}
        </fieldset>

        <div className="mkt-storage__actions">
          <Button type="submit" loading={saving} disabled={!dirty || saving}>
            {t("common.save")}
          </Button>
          {dirty && <span className="mkt-muted">{t("storage.saveHint")}</span>}
        </div>
      </form>

      <Reachability rows={doc.reachability} />

      <DataLocationSection pollMs={pollMs} />

      <BackupsSection onRestartNeeded={setBackupsRestart} />

      <Modal
        open={phase === "confirm"}
        onClose={() => setPhase("idle")}
        title={t("storage.restart.confirmTitle")}
        footer={
          <>
            <Button type="button" variant="ghost" onClick={() => setPhase("idle")}>
              {t("common.cancel")}
            </Button>
            <Button type="button" onClick={() => void restart()}>
              {t("storage.restart.button")}
            </Button>
          </>
        }
      >
        <p>{t("storage.restart.confirmBody")}</p>
      </Modal>
    </section>
  );
}

function Reachability({ rows }: { rows: ReachabilityRow[] }) {
  const { t } = useI18n();
  return (
    <section className="mkt-storage__reach" aria-labelledby="mkt-storage-reach">
      <h3 id="mkt-storage-reach">{t("storage.reach.title")}</h3>
      <p className="mkt-muted mkt-libadmin__small">{t("storage.reach.help")}</p>
      {rows.length === 0 ? (
        <p className="mkt-muted">{t("storage.reach.empty")}</p>
      ) : (
        <div className="mkt-acl__scroll">
          <table className="mkt-table" aria-label={t("storage.reach.title")}>
            <thead>
              <tr>
                <th>{t("storage.reach.library")}</th>
                <th>{t("storage.reach.folder")}</th>
                <th>{t("storage.reach.local")}</th>
                <th>{t("storage.reach.status")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={`${r.library_id}-${r.root}-${i}`}>
                  <td>
                    <bdi>{r.library_name}</bdi>
                  </td>
                  <td>
                    <bdi dir="ltr" className="mkt-libadmin__path">
                      {r.root}
                    </bdi>
                  </td>
                  <td>
                    {r.local ? (
                      <bdi dir="ltr" className="mkt-libadmin__path">
                        {r.local}
                      </bdi>
                    ) : (
                      "—"
                    )}
                  </td>
                  <td>
                    {r.reachable ? (
                      <Badge tone="success">{t("libraryAdmin.folder.reachable")}</Badge>
                    ) : (
                      <Badge tone="error">
                        {t("libraryAdmin.folder.unreachable")}
                        {r.problem ? `: ${r.problem}` : ""}
                      </Badge>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
