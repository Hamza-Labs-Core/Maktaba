// Epic 51 (story 51.10): the words and grouping the Components page, its dialog and its
// drawer share. Phases and reasons are 51.01's words (components.phase.* and
// components.reason.*); a reason is never shown as its raw code.
import { lookupMessage, type Locale } from "../../lib/i18n";
import type { ComponentView } from "../../lib/componentsApi";

type T = (key: string, vars?: Record<string, string | number>) => string;

/** The catalog's groups in page order; `core` comes last as "Always on". */
export const GROUP_ORDER = [
  "media",
  "library",
  "processing",
  "livetv",
  "integrations",
  "cluster",
  "system",
  "runtimes",
  "models",
  "tools",
  "languages",
  "core",
] as const;

/** Phases that are on their way somewhere: the chip shows a spinner. */
export const BUSY_PHASES: ReadonlySet<string> = new Set([
  "starting",
  "draining",
  "installing",
  "removing",
]);

/** Phases during which nothing can be switched (an install or a removal under way). */
export const LOCKED_PHASES: ReadonlySet<string> = new Set(["installing", "removing"]);

export type ChipTone = "success" | "warn" | "error" | "neutral" | "accent";

/** Running and installed green, degraded and waiting amber, failed red, off and stopped grey. */
export function phaseTone(phase: string, kind = ""): ChipTone {
  switch (phase) {
    case "running":
      return "success";
    case "degraded":
    case "waiting":
      return "warn";
    case "failed":
      return "error";
    case "starting":
    case "draining":
    case "installing":
    case "removing":
      return "accent";
    case "off":
      // an asset that is on disk and has nothing to run reads as installed
      return kind === "asset" ? "success" : "neutral";
    default:
      return "neutral";
  }
}

/** The phase in words. An asset that is on disk ("off": nothing to run) reads "Installed". */
export function phaseWord(t: T, phase: string, kind = ""): string {
  if (kind === "asset" && (phase === "off" || phase === "running")) {
    return t("components.page.installed");
  }
  const key = `components.phase.${phase}`;
  const hit = t(key);
  return hit === key ? t("components.phase.unknown") : hit;
}

/** A component's title from its id, for ids that come back in effects and refusals. */
export function titleFinder(t: T, views: readonly ComponentView[]): (id: string) => string {
  const byId = new Map(views.map((v) => [v.id, v]));
  return (id: string) => {
    const v = byId.get(id);
    const key = v?.title_key ?? `components.${id}.title`;
    const hit = t(key);
    return hit === key ? id : hit;
  };
}

/** A list of names joined the locale's way: "A, B and C" / «أ وب وج». */
export function joinNames(locale: Locale, names: string[]): string {
  try {
    return new Intl.ListFormat(locale === "ar" ? "ar" : "en", {
      style: "long",
      type: "conjunction",
    }).format(names);
  } catch {
    return names.join(locale === "ar" ? "، " : ", ");
  }
}

/** The parts of a joined list: each name ("element") and the words between ("literal"). */
export function listParts(
  locale: Locale,
  names: string[]
): { type: "element" | "literal"; value: string }[] {
  try {
    return new Intl.ListFormat(locale === "ar" ? "ar" : "en", {
      style: "long",
      type: "conjunction",
    }).formatToParts(names);
  } catch {
    const sep = locale === "ar" ? "، " : ", ";
    return names.flatMap((value, i) => [
      ...(i > 0 ? [{ type: "literal" as const, value: sep }] : []),
      { type: "element" as const, value },
    ]);
  }
}

/**
 * A reason code in words. Codes may carry an id after a colon (the API's
 * `dependency_off:llm`, `want_missing:model.cohere-arabic`); it becomes the
 * {component} placeholder. An unknown code gives "" rather than the code. A condition's
 * params (`toml_superseded` {key, value}) fill the placeholders they name; a param that
 * is a component's id reads as its title, never the raw id.
 */
export function reasonText(
  t: T,
  locale: Locale,
  reason: string | undefined,
  titleOf: (id: string) => string,
  params?: Record<string, unknown>
): string {
  if (!reason) return "";
  const [code, arg] = reason.split(":", 2) as [string, string | undefined];
  const key = `components.reason.${code}`;
  const template = lookupMessage(locale, key);
  if (template === undefined) return "";
  // the id comes after the colon (the API's form) or in the params (the supervisor's
  // {component} and the runners' {components: [...]}); without one, the text says nothing
  const ids = arg ? [arg] : paramIds(params);
  const name = ids.length ? joinNames(locale, ids.map(titleOf)) : "";
  return filled(
    t(key, {
      value: arg ?? "",
      ...namedParams(params, titleOf),
      ...(name ? { component: name, components: name } : {}),
    })
  );
}

/** The component ids a reason's params name: {component: id} or {components: [ids]}. */
function paramIds(params: Record<string, unknown> | undefined): string[] {
  const one = params?.component;
  if (typeof one === "string" && one) return [one];
  const many = params?.components;
  if (Array.isArray(many))
    return many.filter((v): v is string => typeof v === "string" && v !== "");
  if (typeof many === "string" && many) return [many];
  return [];
}

/**
 * A reason's params with each value that is a component's id (pack_not_published's
 * {name}: `runtime.ml`) replaced by that component's title in the page's language.
 */
function namedParams(
  params: Record<string, unknown> | undefined,
  titleOf: (id: string) => string
): Record<string, string | number> {
  const vars = paramVars(params);
  for (const [k, v] of Object.entries(vars)) {
    if (typeof v === "string" && v) vars[k] = titleOf(v);
  }
  return vars;
}

/** The string and number params of a status or progress, as a message's variables. */
export function paramVars(
  params?: Record<string, unknown> | null
): Record<string, string | number> {
  const vars: Record<string, string | number> = {};
  for (const [k, v] of Object.entries(params ?? {})) {
    if (typeof v === "string" || typeof v === "number") vars[k] = v;
  }
  return vars;
}

/** A message with a placeholder nothing filled says nothing, never a raw `{name}`. */
function filled(text: string): string {
  return /\{\w+\}/.test(text) ? "" : text;
}

/**
 * An install step in words (components.detail.<code>): the progress's name and byte counts
 * and its params fill the placeholders; "" for an unknown code or one left unfilled.
 */
export function detailText(
  t: T,
  locale: Locale,
  p:
    | {
        detail?: string;
        name?: string;
        done?: number;
        total?: number;
        params?: Record<string, unknown>;
      }
    | null
    | undefined,
  size: (bytes: number) => string
): string {
  if (!p?.detail) return "";
  const key = `components.detail.${p.detail}`;
  if (lookupMessage(locale, key) === undefined) return "";
  const vars: Record<string, string | number> = { ...paramVars(p.params) };
  if (p.name) vars.name = p.name;
  if (p.total && p.total > 0) {
    vars.done = size(p.done ?? 0);
    vars.total = size(p.total);
  }
  return filled(t(key, vars));
}

/** Rows grouped in page order, each group in catalog order. Unknown groups go before core. */
export function groupRows(views: readonly ComponentView[]): [string, ComponentView[]][] {
  const by = new Map<string, ComponentView[]>();
  for (const v of views) {
    const list = by.get(v.group) ?? [];
    list.push(v);
    by.set(v.group, list);
  }
  const known: readonly string[] = GROUP_ORDER;
  const order = [
    ...GROUP_ORDER.filter((g) => g !== "core"),
    ...[...by.keys()].filter((g) => !known.includes(g)),
    "core",
  ];
  return order.filter((g) => by.has(g)).map((g) => [g, by.get(g)!]);
}

/**
 * The other machines whose phase differs from the home's own; empty on a home with no
 * other machines, or when all agree. The page's chip is the home's phase.
 */
export function machinesApart(v: ComponentView): string[] {
  if (v.machines.length < 2) return [];
  const home = v.machines.find((m) => m.machine === "home")?.phase ?? v.phase;
  return v.machines.filter((m) => m.machine !== "home" && m.phase !== home).map((m) => m.machine);
}
