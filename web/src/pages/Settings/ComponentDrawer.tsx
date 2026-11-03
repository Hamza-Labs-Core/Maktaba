// Epic 51 (story 51.10): the ⋯ drawer of a component. It reads GET /api/components/{id}:
// each machine's phase and the conditions every part reports, what it requires and what
// needs it, its version and how long it has been in its phase, the install size and the
// last 20 changes from the audit log. Remove and Install live here; they go through the
// same preview and dialog as the row's switch.
//
// Restarts, memory and disk per machine come with the health story (51.14): the status
// rows do not carry them yet.
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Drawer } from "@ds/components/Drawer/Drawer";
import { componentsApi, type ComponentDetail, type ComponentView } from "../../lib/componentsApi";
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
}: {
  view: ComponentView | null;
  /** the list's revision: the drawer reloads when anything changed */
  revision: number;
  titleOf: (id: string) => string;
  disabled: boolean;
  onClose: () => void;
  onAction: (view: ComponentView, action: Action) => void;
}) {
  const { t, locale, formatDate } = useI18n();
  const bytes = useBytes();
  const [detail, setDetail] = useState<ComponentDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const id = view?.id ?? null;

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
  }, [id, revision, t]);

  if (!view) return null;
  const name = titleOf(view.id);
  const d = detail && detail.id === view.id ? detail : null;
  const reason = (r?: string) => reasonText(t, locale, r, titleOf);
  const machineName = (m: string) => (m === "home" ? t("components.drawer.home") : m);
  const requires = view.requires.map((group) =>
    group.length > 1
      ? t("components.drawer.anyOf", { components: joinNames(locale, group.map(titleOf)) })
      : titleOf(group[0] ?? "")
  );

  return (
    <Drawer open onClose={onClose} title={name} className="mkt-comp-drawer">
      <p className="mkt-comp-drawer__desc">{t(view.desc_key)}</p>
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
      </dl>

      {view.switchable && (
        <div className="mkt-comp-drawer__actions">
          {view.want === "absent" ? (
            <Button
              variant="secondary"
              disabled={disabled}
              onClick={() => onAction(view, "install")}
            >
              {t("components.row.install")}
            </Button>
          ) : (
            <Button
              variant="destructive"
              disabled={disabled}
              onClick={() => onAction(view, "remove")}
            >
              {t("components.row.remove")}
            </Button>
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
              {reason(m.reason) && ` · ${reason(m.reason)}`}
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
                          {c.status !== "true" && reason(c.reason) && ` · ${reason(c.reason)}`}
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
              {auditWord(t, a.action)}
              {a.payload?.by && ` · ${a.payload.by}`}
            </li>
          ))}
        </ol>
      )}
    </Drawer>
  );
}

type T = (key: string, vars?: Record<string, string | number>) => string;

function conditionWord(t: T, type: string): string {
  const key = `components.condition.type.${type}`;
  const hit = t(key);
  return hit === key ? type : hit;
}

function auditWord(t: T, action: string): string {
  const key = `components.audit.${action.replace(/^component\./, "")}`;
  const hit = t(key);
  return hit === key ? t("components.audit.other") : hit;
}
