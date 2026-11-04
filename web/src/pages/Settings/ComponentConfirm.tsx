// Epic 51 (story 51.10): the dialog a component change waits in. It shows the preview
// (POST /api/components/preview) in plain words: what else stops or starts, what a
// conflict turns off, how many queued jobs wait and how long running jobs get. A refused
// change shows why and the components in the way, with no confirm button.
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import type { ComponentView, Preview, Refusal } from "../../lib/componentsApi";
import { useI18n } from "../../lib/i18n";
import { joinNames, listParts } from "./componentWords";

/** retry: an asset's failed install asked again (no preview: nothing else changes) */
export type Action = "on" | "off" | "stop" | "start" | "remove" | "install" | "retry";

export interface Plan {
  action: Action;
  view: ComponentView;
  preview: Preview;
}

/** Phases another component moves to when this change takes it down with it. */
const STOPS = new Set(["off", "waiting", "stopped", "not_installed", "draining"]);
const STARTS = new Set(["running", "starting"]);

/** Whether the change touches only its own row and parks nothing: it applies at once. */
export function isSimple(p: Preview, id: string): boolean {
  return (
    p.refused.length === 0 &&
    p.conflicts.length === 0 &&
    p.queued_jobs_parked === 0 &&
    p.effects.every((e) => e.id === id)
  );
}

/** The ids in the way of a refusal (its dependents or its conflicts). */
function blockers(r: Refusal): string[] {
  return r.required_by ?? r.conflicts ?? [];
}

export function ComponentConfirm({
  plan,
  busy,
  titleOf,
  onCancel,
  onConfirm,
  onShow,
}: {
  plan: Plan | null;
  busy: boolean;
  titleOf: (id: string) => string;
  onCancel: () => void;
  onConfirm: () => void;
  /** opens another component's row (a blocker's link) */
  onShow: (id: string) => void;
}) {
  const { t, n, locale } = useI18n();
  if (!plan) return null;
  const { action, view, preview } = plan;
  const name = titleOf(view.id);
  const others = preview.effects.filter((e) => e.id !== view.id);
  const stops = others.filter((e) => STOPS.has(e.to)).map((e) => titleOf(e.id));
  const degrades = others.filter((e) => e.to === "degraded").map((e) => titleOf(e.id));
  const starts = others.filter((e) => STARTS.has(e.to)).map((e) => titleOf(e.id));
  const refused = preview.refused;

  const title = refused.length
    ? t(`components.confirm.refusedTitle.${action}`, { name })
    : t(`components.confirm.title.${action}`, { name });

  // the names joined the locale's way ("A, B and C"), each one a link to its row
  const links = (ids: string[]) => {
    let next = 0;
    return listParts(locale, ids.map(titleOf)).map((part, i) => {
      if (part.type !== "element") return <span key={i}>{part.value}</span>;
      const id = ids[next++]!;
      return (
        <Button key={id} variant="link" onClick={() => onShow(id)}>
          {part.value}
        </Button>
      );
    });
  };

  return (
    <Modal
      open
      onClose={onCancel}
      title={title}
      className="mkt-comp-confirm"
      footer={
        refused.length ? (
          <Button variant="secondary" onClick={onCancel}>
            {t("common.close")}
          </Button>
        ) : (
          <>
            <Button variant="secondary" onClick={onCancel} disabled={busy}>
              {t("common.cancel")}
            </Button>
            <Button
              variant={action === "remove" || action === "off" ? "destructive" : "primary"}
              onClick={onConfirm}
              loading={busy}
            >
              {t(`components.confirm.do.${action}`)}
            </Button>
          </>
        )
      }
    >
      {refused.length > 0 ? (
        <div data-testid="comp-refused">
          {refused.map((r, i) => {
            const ids = blockers(r);
            const text = t(`components.refused.${refusalKey(r.code)}`, {
              name: titleOf(r.id ?? view.id),
              components: "\u0000",
            });
            // the names in the sentence are links to their rows
            const [before, after] = text.split("\u0000") as [string, string | undefined];
            return (
              <p key={`${r.code}-${i}`}>
                {before}
                {after !== undefined && (
                  <>
                    <span className="mkt-comp-confirm__links">{links(ids)}</span>
                    {after}
                  </>
                )}
              </p>
            );
          })}
        </div>
      ) : (
        <div data-testid="comp-effects">
          {preview.conflicts.length > 0 && (
            <p>
              {t("components.confirm.conflicts", {
                name,
                components: joinNames(locale, preview.conflicts.map(titleOf)),
              })}
            </p>
          )}
          {stops.length > 0 && (
            <p>{t("components.confirm.alsoStops", { components: joinNames(locale, stops) })}</p>
          )}
          {degrades.length > 0 && (
            <p>
              {t("components.confirm.alsoDegrades", { components: joinNames(locale, degrades) })}
            </p>
          )}
          {starts.length > 0 && (
            <p>{t("components.confirm.alsoStarts", { components: joinNames(locale, starts) })}</p>
          )}
          {preview.queued_jobs_parked > 0 && (
            <p>
              {preview.queued_jobs_parked === 1
                ? t(`components.confirm.parked.one.${action === "stop" ? "stop" : "off"}`)
                : t(`components.confirm.parked.${action === "stop" ? "stop" : "off"}`, {
                    count: n(preview.queued_jobs_parked),
                  })}
            </p>
          )}
          {preview.drain_s > 0 && (
            <p>{t("components.confirm.drain", { seconds: n(preview.drain_s) })}</p>
          )}
          {action === "remove" && <p>{t("components.confirm.removeKeeps")}</p>}
          {action === "stop" && <p>{t("components.confirm.stopUntil")}</p>}
        </div>
      )}
    </Modal>
  );
}

const KNOWN_REFUSALS = new Set([
  "component_required_by",
  "component_conflicts",
  "licence_missing",
  "not_switchable",
  "stale_generation",
]);

function refusalKey(code: string): string {
  return KNOWN_REFUSALS.has(code) ? code : "other";
}
