// Story 54.44: a plugin's own switch, the same on its card and on its page, for every
// plugin. Turning it off first asks the server what else that touches (other plugins'
// stages that go idle, components that wait or run with less, the loops and processes that
// stop with it, queued jobs that wait) and names each before anything changes. A switch
// that cannot be used is disabled, with why in words.
import { useId, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Toggle } from "@ds/components/Choice/Toggle";
import { Modal } from "@ds/components/Modal/Modal";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { pluginsApi, type Plugin, type SwitchEffect } from "../../lib/pluginsApi";
import { joinNames } from "./componentWords";
import { lockWords, pluginName, refusalWords } from "./pluginWords";

const STOPS = new Set(["off", "waiting", "stopped", "not_installed", "draining"]);

/** Whether turning it off touches nothing but the plugin itself: it applies at once. */
export function effectIsSimple(e: SwitchEffect): boolean {
  return (
    !(e.stages ?? []).length &&
    !(e.also ?? []).length &&
    !(e.loops ?? []).length &&
    !(e.processes ?? []).length &&
    !(e.features ?? []).length &&
    e.queued_jobs_parked === 0
  );
}

export function PluginSwitch({
  plugin: p,
  names,
  onChanged,
}: {
  plugin: Plugin;
  /** another plugin's name, for the dialog (its id when unknown) */
  names: (id: string) => string;
  onChanged: () => void | Promise<void>;
}) {
  const { t, locale, n } = useI18n();
  const why = useId();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [effect, setEffect] = useState<SwitchEffect | null>(null);
  const name = pluginName(p, locale);
  const on = p.want === "enabled";
  const locked = lockWords(t, locale, p);

  const run = async (f: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await f();
      setError("");
      await onChanged();
    } catch (e) {
      setError(refusalWords(t, e, locale));
    } finally {
      setBusy(false);
    }
  };
  const turnOff = async () => {
    let e: SwitchEffect | null = null;
    try {
      e = await pluginsApi.downstream(p.id);
    } catch {
      // the effect could not be read: the switch still works, as it did before
    }
    if (e && !effectIsSimple(e)) setEffect(e);
    else await run(() => pluginsApi.disable(p.id));
  };

  const also = effect?.also ?? [];
  const alsoName = (a: NonNullable<SwitchEffect["also"]>[number]) =>
    bidiIsolate(a.plugin ? names(a.plugin) : t(a.title_key));
  const stops = also.filter((a) => STOPS.has(a.to ?? "waiting")).map(alsoName);
  const degrades = also.filter((a) => a.to === "degraded").map(alsoName);

  return (
    <div className="mkt-plugins__switch">
      <Toggle
        label={t(on ? "plugins.switch.on" : "plugins.switch.off")}
        aria-label={t("plugins.switch.label", { name: bidiIsolate(name) })}
        checked={on}
        disabled={busy || !!locked}
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
      {effect && (
        <Modal
          open
          onClose={() => setEffect(null)}
          title={t("plugins.flow.disable_title", { name: bidiIsolate(name) })}
          footer={
            <>
              <Button variant="ghost" onClick={() => setEffect(null)}>
                {t("common.cancel")}
              </Button>
              <Button
                variant="destructive"
                onClick={() => {
                  setEffect(null);
                  void run(() => pluginsApi.disable(p.id));
                }}
              >
                {t("plugins.action.disable")}
              </Button>
            </>
          }
        >
          {(effect.stages ?? []).length > 0 && (
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
          {(effect.features ?? []).length > 0 && (
            <p data-testid="effect-features">
              {t("plugins.switch.features", {
                features: joinNames(
                  locale,
                  (effect.features ?? []).map((f) => t(`components.feature.${f}`))
                ),
              })}
            </p>
          )}
          {(effect.loops ?? []).length + (effect.processes ?? []).length > 0 && (
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
          {effect.queued_jobs_parked > 0 && (
            <p>
              {effect.queued_jobs_parked === 1
                ? t("components.confirm.parked.one.off")
                : t("components.confirm.parked.off", { count: n(effect.queued_jobs_parked) })}
            </p>
          )}
          {effect.drain_s > 0 && (
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
