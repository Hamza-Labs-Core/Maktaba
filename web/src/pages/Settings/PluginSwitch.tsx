// Story 54.44: a plugin's own switch, the same on its card and on its page, for every
// plugin.
//
// Turning it off first asks the server what else that touches (other plugins' stages that
// go idle, components that wait or run with less, the stages it feeds, the loops and
// processes that stop with it, queued jobs that wait) and names each before anything
// changes. When that cannot be read, nothing is turned off: the dialog says so and offers
// to try again or to turn it off anyway.
//
// A lock (no licence, a stop, withdrawn, incompatible …) stops turning a plugin on, with
// why in words. It never stops turning one off.
//
// Turning on a plugin that cannot run with something that is on names what is in the way
// and offers to switch, as the Components page does.
import { useId, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Toggle } from "@ds/components/Choice/Toggle";
import { Modal } from "@ds/components/Modal/Modal";
import { ApiError } from "../../lib/api";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { pluginErrorCode, pluginsApi, type Plugin, type SwitchEffect } from "../../lib/pluginsApi";
import { joinNames } from "./componentWords";
import { lockWords, pluginName, refusalWords, stageTitle } from "./pluginWords";

const STOPS = new Set(["off", "waiting", "stopped", "not_installed", "draining"]);

/** Whether turning it off touches nothing but the plugin itself: it applies at once. */
export function effectIsSimple(e: SwitchEffect): boolean {
  return (
    !(e.stages ?? []).length &&
    !(e.also ?? []).length &&
    !(e.fed ?? []).length &&
    !(e.loops ?? []).length &&
    !(e.processes ?? []).length &&
    !(e.features ?? []).length &&
    e.queued_jobs_parked === 0
  );
}

interface Named {
  component: string;
  title_key: string;
  plugin?: string;
}

/** What the turn-off dialog shows: the effect, or that it could not be read. */
type Asking = { effect: SwitchEffect } | { unread: true };

export function PluginSwitch({
  plugin: p,
  names,
  onChanged,
}: {
  plugin: Plugin;
  /** another plugin's name, for the dialogs (its id when unknown) */
  names: (id: string) => string;
  onChanged: () => void | Promise<void>;
}) {
  const { t, locale, n } = useI18n();
  const why = useId();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [asking, setAsking] = useState<Asking | null>(null);
  const [conflicts, setConflicts] = useState<Named[] | null>(null);
  const name = pluginName(p, locale);
  const on = p.want === "enabled";
  const locked = lockWords(t, locale, p);
  const nameOf = (a: Named) => bidiIsolate(a.plugin ? names(a.plugin) : t(a.title_key));

  const run = async (f: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await f();
      setError("");
      await onChanged();
    } catch (e) {
      // on: something that is on cannot run beside it; name it and offer to switch
      const inWay =
        e instanceof ApiError && pluginErrorCode(e) === "component_conflicts"
          ? (e.problem as unknown as { conflicts?: Named[] }).conflicts
          : undefined;
      if (inWay?.length) setConflicts(inWay);
      else setError(refusalWords(t, e, locale));
    } finally {
      setBusy(false);
    }
  };
  /** Reads what turning it off touches, then asks or applies. Never turns off unread. */
  const turnOff = async () => {
    if (busy) return;
    setBusy(true);
    let e: SwitchEffect;
    try {
      e = await pluginsApi.downstream(p.id);
    } catch {
      setBusy(false);
      setAsking({ unread: true });
      return;
    }
    setBusy(false);
    if (effectIsSimple(e)) await run(() => pluginsApi.disable(p.id));
    else setAsking({ effect: e });
  };

  const effect = asking && "effect" in asking ? asking.effect : null;
  const also = effect?.also ?? [];
  const stops = also.filter((a) => STOPS.has(a.to ?? "waiting")).map(nameOf);
  const degrades = also.filter((a) => a.to === "degraded").map(nameOf);
  const fed = (effect?.fed ?? []).map((f) => {
    const stage = stageTitle(t, f.stage, f.label);
    return f.plugin
      ? t("plugins.switch.fed_of", { stage, plugin: bidiIsolate(names(f.plugin)) })
      : stage;
  });

  return (
    <div className="mkt-plugins__switch">
      <Toggle
        label={t(on ? "plugins.switch.on" : "plugins.switch.off")}
        aria-label={t("plugins.switch.label", { name: bidiIsolate(name) })}
        checked={on}
        // a lock stops turning it on only: a plugin that is on can always be turned off
        disabled={busy || (!!locked && !on)}
        aria-busy={busy || undefined}
        aria-describedby={locked ? why : undefined}
        onChange={(e) => void (e.target.checked ? run(() => pluginsApi.enable(p.id)) : turnOff())}
      />
      {locked && (
        <p id={why} className="mkt-plugins__hint" data-testid="switch-locked">
          {locked}
        </p>
      )}
      {p.switch_locked === "held" && (
        <Button
          size="sm"
          variant="secondary"
          disabled={busy || !!p.revoked}
          onClick={() => void run(() => pluginsApi.start(p.id))}
        >
          {t("plugins.action.start")}
        </Button>
      )}
      {error && <p role="alert">{error}</p>}
      {conflicts && (
        <Modal
          open
          onClose={() => setConflicts(null)}
          title={t("components.confirm.title.on", { name: bidiIsolate(name) })}
          footer={
            <>
              <Button variant="ghost" onClick={() => setConflicts(null)}>
                {t("common.cancel")}
              </Button>
              <Button
                onClick={() => {
                  setConflicts(null);
                  void run(() => pluginsApi.enable(p.id, true));
                }}
              >
                {t("components.confirm.do.on")}
              </Button>
            </>
          }
        >
          <p data-testid="switch-conflicts">
            {t("components.confirm.conflicts", {
              name: bidiIsolate(name),
              components: joinNames(locale, conflicts.map(nameOf)),
            })}
          </p>
        </Modal>
      )}
      {asking && (
        <Modal
          open
          onClose={() => setAsking(null)}
          title={t("plugins.flow.disable_title", { name: bidiIsolate(name) })}
          footer={
            <>
              <Button variant="ghost" onClick={() => setAsking(null)}>
                {t("common.cancel")}
              </Button>
              {!effect && (
                <Button
                  variant="secondary"
                  onClick={() => {
                    setAsking(null);
                    void turnOff();
                  }}
                >
                  {t("plugins.switch.unread_retry")}
                </Button>
              )}
              <Button
                variant="destructive"
                onClick={() => {
                  setAsking(null);
                  void run(() => pluginsApi.disable(p.id));
                }}
              >
                {effect ? t("plugins.action.disable") : t("plugins.switch.unread_anyway")}
              </Button>
            </>
          }
        >
          {!effect && (
            <p role="alert" data-testid="effect-unread">
              {t("plugins.switch.unread")}
            </p>
          )}
          {effect && (effect.stages ?? []).length > 0 && (
            <>
              <p>{t("plugins.flow.disable_body")}</p>
              <ul>
                {(effect.stages ?? []).map((s) => (
                  <li key={s} dir="ltr">
                    {s}
                  </li>
                ))}
              </ul>
            </>
          )}
          {stops.length > 0 && (
            <p data-testid="effect-stops">
              {t("components.confirm.alsoStops", { components: joinNames(locale, stops) })}
            </p>
          )}
          {degrades.length > 0 && (
            <p data-testid="effect-degrades">
              {t("components.confirm.alsoDegrades", { components: joinNames(locale, degrades) })}
            </p>
          )}
          {fed.length > 0 && (
            <p data-testid="effect-fed">
              {t("plugins.switch.fed", { stages: joinNames(locale, fed) })}
            </p>
          )}
          {effect && (effect.features ?? []).length > 0 && (
            <p data-testid="effect-features">
              {t("plugins.switch.features", {
                features: joinNames(
                  locale,
                  (effect.features ?? []).map((f) => t(`components.feature.${f}`))
                ),
              })}
            </p>
          )}
          {effect && (effect.loops ?? []).length + (effect.processes ?? []).length > 0 && (
            <>
              <p>{t("plugins.switch.parts")}</p>
              <ul data-testid="effect-parts">
                {[...(effect.processes ?? []), ...(effect.loops ?? [])].map((x) => (
                  <li key={x} dir="ltr">
                    {x}
                  </li>
                ))}
              </ul>
            </>
          )}
          {effect && effect.queued_jobs_parked > 0 && (
            <p>
              {effect.queued_jobs_parked === 1
                ? t("components.confirm.parked.one.off")
                : t("components.confirm.parked.off", { count: n(effect.queued_jobs_parked) })}
            </p>
          )}
          {effect && effect.drain_s > 0 && (
            <p>{t("components.confirm.drain", { seconds: n(effect.drain_s) })}</p>
          )}
          <p className="mkt-plugins__hint">
            <Link to="/settings/components">{t("plugins.switch.components_link")}</Link>
          </p>
        </Modal>
      )}
    </div>
  );
}
