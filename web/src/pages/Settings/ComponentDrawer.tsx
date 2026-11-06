// Epic 51 (story 51.10): the ⋯ drawer of a component. It reads GET /api/components/{id}:
// each machine's phase and the conditions every part reports, what it requires and what
// needs it, its version and how long it has been in its phase, the install size and the
// last 20 changes from the audit log. Remove and Install live here (and Retry, for an
// asset whose install failed); they go through the
// same preview and dialog as the row's switch.
//
// Each machine's health (story 51.14) shows under it: how long it has been running, its
// restarts in the last 24 hours, its memory and its files on disk, and the last error. The
// server.toml keys it owns are listed; a key the database overrides says so on the config
// part's condition. Reset settings (51.13) opens the page's confirm dialog.
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { logsPageLink } from "../../lib/logIndex";
import { Button } from "@ds/components/Button/Button";
import { Drawer } from "@ds/components/Drawer/Drawer";
import {
  componentsApi,
  restartsOf,
  type ComponentDetail,
  type ComponentView,
  type Health,
} from "../../lib/componentsApi";
import { errorText } from "../../lib/errorText";
import { useI18n } from "../../lib/i18n";
import { useBytes } from "../../components/queue/offline";
import { LinesPlaceholder } from "../../components/Placeholders";
import { joinNames, phaseWord, reasonText } from "./componentWords";
import type { Action } from "./ComponentConfirm";

export function ComponentDrawer({
  view,
  revision,
  titleOf,
  disabled,
  onClose,
  onAction,
  onResetSettings,
}: {
  view: ComponentView | null;
  /** the list's revision: the drawer reloads when it, or the row's status, changes */
  revision: number;
  titleOf: (id: string) => string;
  disabled: boolean;
  onClose: () => void;
  onAction: (view: ComponentView, action: Action) => void;
  /** asks to reset its rules sections to the defaults (it owns at least one) */
  onResetSettings: (view: ComponentView) => void;
}) {
  const { t, locale, formatDate } = useI18n();
  const bytes = useBytes();
  const [detail, setDetail] = useState<ComponentDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const id = view?.id ?? null;
  // the row's status as the list read it: an install's end or a phase a machine reports
  // changes it but not the desired state's revision, and the drawer follows it as the row does
  const live = view ? JSON.stringify(view) : "";

  useEffect(() => {
    if (!id) {
      setDetail(null);
      return;
    }
    let alive = true;
    componentsApi
      .show(id)
      .then((d) => {
        if (!alive) return;
        setDetail(d);
        setError(null);
      })
      .catch((e: unknown) => {
        if (alive) setError(errorText(e, t));
      });
    return () => {
      alive = false;
    };
  }, [id, revision, live, t]);

  if (!view) return null;
  const name = titleOf(view.id);
  const d = detail && detail.id === view.id ? detail : null;
  const reason = (r?: string, params?: Record<string, unknown>) =>
    reasonText(t, locale, r, titleOf, params);
  const tomlKeys = view.settings.filter((k) => k.startsWith("toml:")).map((k) => k.slice(5));
  const ownsRules = view.settings.some((k) => k.startsWith("rules."));
  const ask = assetAsk(view);
  const machineName = (m: string) => (m === "home" ? t("components.drawer.home") : m);
  const requires = view.requires.map((group) =>
    group.length > 1
      ? t("components.drawer.anyOf", { components: joinNames(locale, group.map(titleOf)) })
      : titleOf(group[0] ?? "")
  );

  return (
    <Drawer
      open
      onClose={onClose}
      title={name}
      className="mkt-comp-drawer"
      closeLabel={t("common.close")}
    >
      <p className="mkt-comp-drawer__desc">{t(view.desc_key)}</p>
      {/* Epic 53 story 53.11: this component's lines and errors */}
      <p className="mkt-comp-drawer__links">
        <Link to={logsPageLink({ components: view.id })}>{t("nav.admin.logs")}</Link>
        {" · "}
        <Link to={`/admin/errors?component=${encodeURIComponent(view.id)}`}>
          {t("nav.admin.errors")}
        </Link>
      </p>
      {view.hold && (
        <p>
          {t("components.drawer.heldBy", {
            who: view.hold.by,
            when: formatDate(view.hold.at),
          })}
        </p>
      )}
      <dl className="mkt-comp-drawer__facts">
        {requires.length > 0 && (
          <>
            <dt>{t("components.drawer.requires")}</dt>
            <dd>{requires.join(locale === "ar" ? "، " : ", ")}</dd>
          </>
        )}
        {view.wants.length > 0 && (
          <>
            <dt>{t("components.drawer.wants")}</dt>
            <dd>{joinNames(locale, view.wants.map(titleOf))}</dd>
          </>
        )}
        {view.dependents.length > 0 && (
          <>
            <dt>{t("components.drawer.dependents")}</dt>
            <dd>{joinNames(locale, view.dependents.map(titleOf))}</dd>
          </>
        )}
        {view.conflicts.length > 0 && (
          <>
            <dt>{t("components.drawer.conflicts")}</dt>
            <dd>{joinNames(locale, view.conflicts.map(titleOf))}</dd>
          </>
        )}
        {view.install_size_mb > 0 && (
          <>
            <dt>{t("components.drawer.size")}</dt>
            <dd>
              {t("components.page.onDisk", { size: bytes(view.install_size_mb * 1024 * 1024) })}
            </dd>
          </>
        )}
        {tomlKeys.length > 0 && (
          <>
            <dt>{t("components.drawer.toml")}</dt>
            <dd>
              {tomlKeys.map((k, i) => (
                <span key={k}>
                  {i > 0 && (locale === "ar" ? "، " : ", ")}
                  <code dir="ltr">{k}</code>
                </span>
              ))}
            </dd>
          </>
        )}
      </dl>

      {(view.switchable || ownsRules) && (
        <div className="mkt-comp-drawer__actions">
          {ownsRules && (
            <Button variant="secondary" disabled={disabled} onClick={() => onResetSettings(view)}>
              {t("components.settings.reset")}
            </Button>
          )}
          {!view.switchable ? null : view.want === "absent" ? (
            <Button
              variant="secondary"
              disabled={disabled}
              onClick={() => onAction(view, "install")}
            >
              {t("components.row.install")}
            </Button>
          ) : (
            <>
              {ask && (
                <Button variant="secondary" disabled={disabled} onClick={() => onAction(view, ask)}>
                  {t(`components.row.${ask}`)}
                </Button>
              )}
              <Button
                variant="destructive"
                disabled={disabled}
                onClick={() => onAction(view, "remove")}
              >
                {t("components.row.remove")}
              </Button>
            </>
          )}
        </div>
      )}

      <h3>{t("components.drawer.machines")}</h3>
      {error && <p role="alert">{error}</p>}
      {!d && !error && <LinesPlaceholder lines={3} />}
      {d && (
        <ul className="mkt-comp-drawer__machines">
          {d.machines.map((m) => (
            <li key={m.machine}>
              <strong>{machineName(m.machine)}</strong>
              {": "}
              {phaseWord(t, m.phase, view.kind)}
              {reason(m.reason, m.reason_params ?? undefined) &&
                ` · ${reason(m.reason, m.reason_params ?? undefined)}`}
              {m.health && <HealthFacts health={m.health} kind={view.kind} reason={reason} />}
              {(m.parts ?? []).map((p) => (
                <div key={p.part} className="mkt-comp-drawer__part">
                  <span>
                    {t("components.drawer.part", {
                      part: p.part,
                      phase: phaseWord(t, p.phase, view.kind),
                      since: formatDate(p.since),
                    })}
                  </span>
                  {p.version && (
                    <span>
                      {" · "}
                      {t("components.drawer.version", { version: p.version })}
                    </span>
                  )}
                  {p.conditions.length > 0 && (
                    <ul>
                      {p.conditions.map((c) => (
                        <li key={c.type}>
                          {t(
                            `components.condition.${c.status === "true" ? "met" : c.status === "false" ? "unmet" : "unknown"}`,
                            {
                              condition: conditionWord(t, c.type),
                            }
                          )}
                          {c.status !== "true" &&
                            reason(c.reason, c.params) &&
                            ` · ${typeof c.params?.key === "string" ? `${c.params.key}: ` : ""}${reason(c.reason, c.params)}`}
                          {c.status !== "true" &&
                            !reason(c.reason, c.params) &&
                            typeof c.params?.component === "string" &&
                            ` · ${t("components.condition.waitingOn", { component: titleOf(c.params.component) })}`}
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              ))}
            </li>
          ))}
        </ul>
      )}

      <h3>{t("components.drawer.changes")}</h3>
      {d && d.audit.length === 0 && <p>{t("components.drawer.noChanges")}</p>}
      {d && d.audit.length > 0 && (
        <ol className="mkt-comp-drawer__audit">
          {d.audit.map((a) => (
            <li key={a.id}>
              <time dateTime={a.at}>{formatDate(a.at)}</time>
              {" · "}
              {auditWord(t, a.action, view.kind)}
              {a.payload?.by && ` · ${a.payload.by}`}
            </li>
          ))}
        </ol>
      )}
    </Drawer>
  );
}

type T = (key: string, vars?: Record<string, string | number>) => string;

/**
 * What a wanted asset's drawer offers besides Remove: Install when it is not on this home
 * (a fresh home's runtime or model), Retry when its last install failed; nothing while
 * it installs or removes, when it is there, or when packs are off on this machine.
 */
function assetAsk(v: ComponentView): "install" | "retry" | null {
  if (v.kind !== "asset" || !v.switchable || v.want === "absent") return null;
  const p = v.progress;
  if (p?.busy || p?.installed || v.phase === "installing" || v.phase === "removing") return null;
  if (v.reason === "packs_disabled") return null;
  if (v.phase === "failed" || p?.phase === "failed") return "retry";
  return v.phase === "not_installed" ? "install" : null;
}

/** One machine's uptime, restarts (24 h), memory, disk and last error (story 51.14). */
function HealthFacts({
  health,
  kind,
  reason,
}: {
  health: Health;
  kind: string;
  reason: (r?: string) => string;
}) {
  const { t, n, formatDate } = useI18n();
  const bytes = useBytes();
  const runs = kind.split("+").some((k) => k === "process" || k === "loop");
  const mb = (v: number) => bytes(v * 1024 * 1024);
  const uptime = health.since ? (Date.now() - Date.parse(health.since)) / 1000 : null;
  return (
    <div className="mkt-comp-drawer__health" data-testid="comp-health">
      <dl className="mkt-comp-drawer__facts">
        {uptime !== null && Number.isFinite(uptime) && (
          <>
            <dt>{t("components.drawer.uptime")}</dt>
            <dd>{humanDuration(uptime, t, n)}</dd>
          </>
        )}
        {runs && (
          <>
            <dt>{t("components.drawer.restarts")}</dt>
            <dd>{n(restartsOf(health))}</dd>
          </>
        )}
        {health.rss_mb != null && (
          <>
            <dt>{t("components.drawer.memory")}</dt>
            <dd>{mb(health.rss_mb)}</dd>
          </>
        )}
        {health.disk_mb != null && (
          <>
            <dt>{t("components.drawer.disk")}</dt>
            <dd>{mb(health.disk_mb)}</dd>
          </>
        )}
        {health.last_error_code && (
          <>
            <dt>{t("components.drawer.lastError")}</dt>
            <dd>
              {reason(health.last_error_code) || t("components.phase.failed")}
              {health.last_error_at && (
                <>
                  {" · "}
                  <time dateTime={health.last_error_at}>{formatDate(health.last_error_at)}</time>
                </>
              )}
            </dd>
          </>
        )}
      </dl>
      {health.stale && <p className="mkt-comp-drawer__stale">{t("components.drawer.stale")}</p>}
    </div>
  );
}

/** Seconds as "2d 3h" / "4h 10m" / "5m" / "12s" (the Processes page's words). */
function humanDuration(sec: number, t: T, n: (v: number) => string): string {
  const s = Math.max(0, Math.floor(sec));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return t("health.processes.dur.days", { d: n(d), h: n(h) });
  if (h > 0) return t("health.processes.dur.hours", { h: n(h), m: n(m) });
  if (m > 0) return t("health.processes.dur.minutes", { m: n(m) });
  return t("health.processes.dur.seconds", { s: n(s) });
}

function conditionWord(t: T, type: string): string {
  const key = `components.condition.type.${type}`;
  const hit = t(key);
  return hit === key ? type : hit;
}

function auditWord(t: T, action: string, kind: string): string {
  // an asset has no run: enabled and installed both mean its files are on disk, so a move
  // between them (or from absent) is not turning it on or off
  if (kind === "asset" && (action === "component.enable" || action === "component.disable")) {
    return t("components.audit.asset.installed");
  }
  const key = `components.audit.${action.replace(/^component\./, "")}`;
  const hit = t(key);
  return hit === key ? t("components.audit.other") : hit;
}
