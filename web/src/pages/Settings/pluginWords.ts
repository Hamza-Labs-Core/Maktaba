// Epic 54: the words the Plugins pages share (the list, a plugin's page, Browse, the
// store account): a refusal's words, a capability's title, a plugin's name and state.
import { ApiError } from "../../lib/api";
import { pluginErrorCode, pluginWord, type Plugin } from "../../lib/pluginsApi";
import { phaseWord, reasonText } from "./componentWords";

export type T = (key: string, vars?: Record<string, string | number>) => string;

/** The words of a refusal: plugins.error.<code>, else the server's detail. */
export function refusalWords(
  t: T,
  e: unknown,
  locale: "en" | "ar" = typeof document !== "undefined" && document.documentElement.lang === "ar"
    ? "ar"
    : "en"
): string {
  // a flow check's refusal carries its own words, the cycle path named (54.39)
  if (e instanceof ApiError) {
    const p = e.problem as unknown as { message?: string; message_ar?: string } | undefined;
    const words = locale === "ar" ? p?.message_ar || p?.message : p?.message;
    if (words) return words;
  }
  const code = pluginErrorCode(e);
  if (code) {
    const key = `plugins.error.${code}`;
    const hit = t(key);
    if (hit !== key) return hit;
  }
  if (e instanceof ApiError) return e.problem?.detail || e.message;
  return e instanceof Error ? e.message : String(e);
}

/** A capability's title in Maktaba's words (capabilities.toml's title_key). */
export function capabilityTitle(t: T, cap: string): { title: string; detail: string } {
  const base = cap.split(":", 1)[0].replace(/[.-]/g, "_");
  const title = t(`plugins.capability.${base}.title`);
  const detail = t(`plugins.capability.${base}.detail`);
  const param = cap.includes(":") ? ` (${cap.slice(cap.indexOf(":") + 1)})` : "";
  return { title: title + param, detail };
}

// anything the store did not countersign (story 54.14 §2; "store" is Phase 1's name)
export const unreviewed = (level: string) =>
  level !== "store" && level !== "automated" && level !== "reviewed";

/** Whether Maktaba publishes the plugin (story 54.44). */
export const byMaktaba = (p: Pick<Plugin, "publisher">) => p.publisher?.id === "maktaba";

/** Whether it came with the Maktaba release: updated with Maktaba, not removed on its own. */
export const withRelease = (p: Pick<Plugin, "source">) => p.source === "release";

export function pluginName(p: Pick<Plugin, "id" | "names">, locale: string): string {
  return pluginWord(p.names?.name, locale) || p.id;
}

/** The phase and its reason in words (Epic 51's), with a breaker's hold said as such. */
export function statusWords(t: T, locale: "en" | "ar", p: Plugin): string {
  if (p.health?.tripped && p.held_by === "plugin-breaker")
    return t("components.reason.plugin_crash_loop");
  if (p.reason === "kept_off" || (p.kept_off && p.want === "installed"))
    return t("components.reason.kept_off");
  const why = reasonText(t, locale, p.reason, (id) => id, p.reason_params ?? undefined);
  return why || phaseWord(t, p.phase || "off");
}

/** Why a plugin cannot be turned on from its switch, in words ("" when it can). */
export function lockWords(t: T, locale: "en" | "ar", p: Plugin): string {
  const code = p.switch_locked;
  if (!code) return "";
  const own = t(`plugins.lock.${code}`);
  if (own !== `plugins.lock.${code}`) return own;
  return (
    reasonText(t, locale, code, (id) => id, p.reason_params ?? undefined) || t("plugins.lock.other")
  );
}

/** A job time for people: 850 ms, 12 s, 3 min. */
export function jobTime(t: T, n: (x: number) => string, ms: number | undefined): string {
  if (ms === undefined || ms === null) return t("plugins.resources.no_time");
  if (ms < 1000) return t("plugins.time.ms", { n: n(Math.round(ms)) });
  if (ms < 90_000) return t("plugins.time.s", { n: n(Math.round(ms / 100) / 10) });
  return t("plugins.time.min", { n: n(Math.round(ms / 6000) / 10) });
}

/** The failures view filtered to these stages (its `stage` filter takes several). */
export function failuresLink(queues: string[]): string {
  return queues.length
    ? `/queue/failures?stage=${queues.map(encodeURIComponent).join(",")}`
    : "/queue/failures";
}

/** A stage's name for people: Maktaba's own word for it, else the name its manifest gives
 * it, and its identifier only when it has neither. */
export function stageTitle(t: T, name: string, label?: string): string {
  const key = `queue.stage.${name}.title`;
  const hit = t(key);
  return hit === key ? label || name : hit;
}
