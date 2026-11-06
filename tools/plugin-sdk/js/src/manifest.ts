// The plugin manifest validator for the JavaScript and TypeScript SDK (Epic 54, story
// 54.01): the schema of shared/plugins/manifest.schema.json, then the same rules across
// fields in the same order as the Go, Rust and Python validators, and the same normalised
// JSON byte for byte (the corpus in shared/plugins/testdata/manifests).

import { createHash } from "node:crypto";
import { parse as parseToml } from "smol-toml";

import { capabilitiesToml, eventsToml, manifestSchema } from "./data.generated.js";

export const CODES = {
  invalid: "manifest_invalid",
  unknownKey: "manifest_unknown_key",
  missingKey: "manifest_missing_key",
  schemaNewer: "manifest_schema_newer",
  reservedId: "manifest_reserved_id",
  badPlatform: "manifest_bad_platform",
  badRange: "manifest_bad_range",
  badCapability: "manifest_bad_capability",
  duplicateCapability: "manifest_duplicate_capability",
  duplicateStage: "manifest_duplicate_stage",
  badRequest: "manifest_bad_request",
  unknownEvent: "manifest_unknown_event",
  subjectMismatch: "manifest_subject_mismatch",
  badSchedule: "manifest_bad_schedule",
  missingString: "manifest_missing_string",
  stringTooLong: "manifest_string_too_long",
  missingFile: "manifest_missing_file",
  badSettings: "manifest_bad_settings",
  notToml: "manifest_not_toml",
} as const;

const SCHEMA_VERSION = 1;
const MAX_NAME = 40;
const MAX_SUMMARY = 140;

export interface ManifestError {
  code: string;
  path: string;
  detail: string;
}

export class ManifestInvalid extends Error {
  constructor(public readonly errors: ManifestError[]) {
    super(errors.map((e) => `${e.code} at ${e.path}: ${e.detail}`).join("; "));
  }
  has(code: string): boolean {
    return this.errors.some((e) => e.code === code);
  }
}

/** A file of the package by its path (i18n/en.json, ui/…), or undefined. */
export type Files = (path: string) => Uint8Array | string | undefined;

// TOML integers are read as bigint (integersAsBigInt), so 1 and 1.0 stay apart as in the
// other three validators: bigint is an integer, number a float (or a schema default).
type Json = null | boolean | number | bigint | string | Json[] | { [k: string]: Json };
type Obj = { [k: string]: Json };

interface SchemaNode {
  type?: string | string[];
  properties?: Record<string, SchemaNode>;
  additionalProperties?: boolean;
  required?: string[];
  enum?: Json[];
  pattern?: string;
  minLength?: number;
  maxLength?: number;
  minimum?: number;
  maximum?: number;
  items?: SchemaNode;
  minItems?: number;
  maxItems?: number;
  uniqueItems?: boolean;
  default?: Json;
  "x-code"?: string;
}

const KEYWORDS = new Set([
  "$schema", "$id", "title", "description", "type", "properties", "additionalProperties",
  "required", "enum", "pattern", "minLength", "maxLength", "minimum", "maximum", "items",
  "minItems", "maxItems", "uniqueItems", "default", "x-code", "x-unknown-code",
]);

interface Capability {
  name: string;
  risk: string;
  interface: string;
  functions: string[];
  param?: string;
  title_key: string;
  detail_key: string;
  since: string;
}

interface PluginEvent {
  name: string;
  subject: string;
  stage: string;
  map: string;
  trigger?: boolean;
  since: string;
}

let loaded: {
  schema: SchemaNode;
  caps: Map<string, Capability>;
  events: Map<string, PluginEvent>;
} | null = null;

function load() {
  if (loaded) return loaded;
  const schema = JSON.parse(manifestSchema) as SchemaNode;
  const check = (n: SchemaNode) => {
    for (const k of Object.keys(n)) {
      if (!KEYWORDS.has(k)) throw new Error(`schema keyword ${k} is not read by the validators`);
    }
    for (const c of Object.values(n.properties ?? {})) check(c);
    if (n.items) check(n.items);
  };
  check(schema);
  const caps = new Map<string, Capability>();
  for (const c of (parseToml(capabilitiesToml) as unknown as { capability: Capability[] }).capability) {
    caps.set(c.name, c);
  }
  const events = new Map<string, PluginEvent>();
  for (const e of (parseToml(eventsToml) as unknown as { event: PluginEvent[] }).event) {
    events.set(e.name, e);
  }
  loaded = { schema, caps, events };
  return loaded;
}

export function capabilities(): Capability[] {
  return [...load().caps.values()];
}

export function events(): PluginEvent[] {
  return [...load().events.values()];
}

/** Go's and Rust's `$`: the end of the text (JavaScript's already is, without the m flag). */
function matches(pattern: string, s: string): boolean {
  return new RegExp(pattern, "u").test(s);
}

export function validId(id: string): boolean {
  const p = load().schema.properties?.id?.pattern ?? "";
  return id.length <= 100 && matches(p, id);
}

/** A plugin stage's job_stages.name (54.01 §4): p_<8 hex of sha256(id)>_<stage>. */
export function queueName(pluginId: string, stage: string): string {
  return `p_${createHash("sha256").update(pluginId).digest("hex").slice(0, 8)}_${stage}`;
}

function typeOf(v: unknown): string {
  if (typeof v === "boolean") return "boolean";
  if (typeof v === "number") return "number";
  if (typeof v === "bigint") return "integer";
  if (typeof v === "string") return "string";
  if (Array.isArray(v)) return "array";
  if (v !== null && typeof v === "object") {
    // smol-toml's tables have no prototype; a TOML date is a Date and is refused
    const proto = Object.getPrototypeOf(v);
    if (proto === Object.prototype || proto === null) return "object";
  }
  return "unknown";
}

function canonical(v: Json): string {
  if (Array.isArray(v)) return `[${v.map(canonical).join(",")}]`;
  if (v !== null && typeof v === "object") {
    return `{${Object.keys(v)
      .sort(byCodeUnits)
      .map((k) => `${JSON.stringify(k)}:${canonical((v as Obj)[k] as Json)}`)
      .join(",")}}`;
  }
  if (typeof v === "bigint") return v.toString();
  return JSON.stringify(v);
}

// Go, Python and Rust sort keys by bytes (code points); for the manifest's ASCII keys
// UTF-16 code units give the same order.
function byCodeUnits(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

function equal(a: Json, b: Json): boolean {
  return canonical(a) === canonical(b);
}

function join(path: string, el: string): string {
  return path === "" ? el : `${path}/${el}`;
}

function check(n: SchemaNode, v: Json, path: string, parent: string, errs: ManifestError[]): void {
  const code = n["x-code"] || parent || CODES.invalid;
  const t = typeOf(v);
  if (n.type !== undefined) {
    const wants = typeof n.type === "string" ? [n.type] : n.type;
    if (!wants.some((w) => w === t || (w === "number" && t === "integer"))) {
      errs.push({ code, path, detail: `type ${t}` });
      return;
    }
  }
  if (n.enum && !n.enum.some((e) => equal(e, v))) {
    errs.push({ code, path, detail: "not one of the allowed values" });
    return;
  }
  if (t === "string") {
    const s = v as string;
    const count = [...s].length;
    if ((n.minLength !== undefined && count < n.minLength) || (n.maxLength !== undefined && count > n.maxLength)) {
      errs.push({ code, path, detail: "length" });
      return;
    }
    if (n.pattern !== undefined && !matches(n.pattern, s)) errs.push({ code, path, detail: "pattern" });
  } else if (t === "integer" || t === "number") {
    const x = Number(v);
    if ((n.minimum !== undefined && x < n.minimum) || (n.maximum !== undefined && x > n.maximum)) {
      errs.push({ code, path, detail: "range" });
    }
  } else if (t === "array") {
    const a = v as Json[];
    if ((n.minItems !== undefined && a.length < n.minItems) || (n.maxItems !== undefined && a.length > n.maxItems)) {
      errs.push({ code, path, detail: "items" });
      return;
    }
    if (n.uniqueItems) {
      for (let i = 0; i < a.length; i++) {
        for (let j = 0; j < i; j++) {
          if (equal(a[i] as Json, a[j] as Json)) {
            errs.push({ code, path, detail: "duplicate item" });
            return;
          }
        }
      }
    }
    if (n.items) a.forEach((x, i) => check(n.items as SchemaNode, x, join(path, String(i)), code, errs));
  } else if (t === "object") {
    const o = v as Obj;
    for (const r of n.required ?? []) {
      if (!(r in o)) errs.push({ code: CODES.missingKey, path: join(path, r), detail: "" });
    }
    const props = n.properties ?? {};
    for (const k of Object.keys(o).sort(byCodeUnits)) {
      const c = props[k];
      if (!c) {
        if (n.additionalProperties === false) errs.push({ code: CODES.unknownKey, path: join(path, k), detail: "" });
        continue;
      }
      check(c, o[k] as Json, join(path, k), code, errs);
    }
  }
}

function fill(n: SchemaNode, v: Json): Json {
  if (typeOf(v) === "object") {
    const o = v as Obj;
    const props = n.properties ?? {};
    for (const k of Object.keys(props).sort(byCodeUnits)) {
      const c = props[k] as SchemaNode;
      if (!(k in o) && c.default !== undefined) o[k] = JSON.parse(JSON.stringify(c.default)) as Json;
      if (k in o) o[k] = fill(c, o[k] as Json);
    }
  } else if (Array.isArray(v) && n.items) {
    v.forEach((x, i) => (v[i] = fill(n.items as SchemaNode, x)));
  }
  return v;
}

/** TOML values as the manifest's own types; dates and times are refused. */
function plain(v: unknown): Json {
  const t = typeOf(v);
  if (t === "object") {
    const o: Obj = {};
    for (const [k, x] of Object.entries(v as object)) o[k] = plain(x);
    return o;
  }
  if (t === "array") return (v as unknown[]).map(plain);
  if (t === "boolean" || t === "integer" || t === "number" || t === "string") return v as Json;
  throw new Error("a date, time or other value is not allowed in a manifest");
}

/** Parse and check a manifest.toml; answer the normalised manifest or throw ManifestInvalid.
 * `files` serves the package's files to the package rules; undefined skips them. */
export function validate(src: Uint8Array | string, files?: Files): Obj {
  const { schema } = load();
  let raw: Obj;
  try {
    const text = typeof src === "string" ? src : new TextDecoder("utf-8", { fatal: true }).decode(src);
    if (text.length > 1 << 20) throw new Error("over 1 MB");
    raw = plain(parseToml(text, { integersAsBigInt: true })) as Obj;
  } catch (e) {
    throw new ManifestInvalid([{ code: CODES.notToml, path: "", detail: String(e) }]);
  }
  if (typeof raw.schema === "bigint" && raw.schema > BigInt(SCHEMA_VERSION)) {
    throw new ManifestInvalid([{ code: CODES.schemaNewer, path: "schema", detail: "" }]);
  }
  let errs: ManifestError[] = [];
  check(schema, raw, "", "", errs);
  if (errs.length) throw new ManifestInvalid(errs);
  const m = fill(schema, raw) as Obj;
  m.maktaba = (m.maktaba as string)
    .split(",")
    .map((p) => p.replaceAll(" ", ""))
    .join(", ");
  errs = rules(m, files);
  if (errs.length) throw new ManifestInvalid(errs);
  return m;
}

/** The one JSON text of a valid manifest: keys sorted, no spaces, UTF-8. */
export function normalised(m: Obj): string {
  return canonical(m);
}

function reserved(id: string): boolean {
  let labels = id.split(".");
  if (id.startsWith("tv.maktaba.community.")) labels = labels.slice(3);
  else if (id.startsWith("tv.maktaba.") || id.startsWith("dev.maktaba.")) return true;
  return labels.some((l) => l === "maktaba" || l === "official");
}

function knownCapability(name: string): boolean {
  const i = name.indexOf(":");
  const base = i < 0 ? name : name.slice(0, i);
  const c = load().caps.get(base);
  if (!c) return false;
  if (!c.param) return i < 0;
  return i >= 0 && validId(name.slice(i + 1));
}

function rules(m: Obj, files?: Files): ManifestError[] {
  const errs: ManifestError[] = [];
  const push = (code: string, path: string, detail = "") => errs.push({ code, path, detail });
  if (reserved(m.id as string)) push(CODES.reservedId, "id");
  const platforms = m.platforms as string[];
  if (platforms.length > 1 && platforms.includes("any")) push(CODES.badPlatform, "platforms", "any stands alone");
  if (!(m.maktaba as string).split(", ").some((p) => p.startsWith(">") || p.startsWith("="))) {
    push(CODES.badRange, "maktaba", "no lower bound");
  }
  const caps = m.capabilities as Obj;
  const seen = new Set<string>();
  for (const list of ["required", "optional"]) {
    (caps[list] as string[]).forEach((name, i) => {
      const path = `capabilities/${list}/${i}`;
      if (!knownCapability(name)) {
        push(CODES.badCapability, path, name);
        return;
      }
      if (seen.has(name)) push(CODES.duplicateCapability, path, name);
      seen.add(name);
    });
  }
  const stages = m.stage as Obj[];
  const subjects = new Map<string, string>();
  stages.forEach((st, i) => {
    if (subjects.has(st.name as string)) push(CODES.duplicateStage, `stage/${i}/name`, st.name as string);
    subjects.set(st.name as string, st.subject as string);
  });
  const evs = load().events;
  stages.forEach((st, i) => {
    (st.may_request as string[]).forEach((r, j) => {
      if (!subjects.has(r)) push(CODES.badRequest, `stage/${i}/may_request/${j}`, r);
    });
    (st.trigger as Obj[]).forEach((t, j) => {
      const ev = t.event as string;
      const path = `stage/${i}/trigger/${j}/event`;
      const e = evs.get(ev);
      if (!e || e.trigger === false) push(CODES.unknownEvent, path, ev);
      else if (e.subject !== st.subject) push(CODES.subjectMismatch, path, ev);
    });
  });
  (m.schedule as Obj[]).forEach((s, i) => {
    if (subjects.get(s.stage as string) !== "catalog") push(CODES.badSchedule, `schedule/${i}/stage`, s.stage as string);
  });
  if (!files || errs.length) return errs;
  return errs.concat(packageRules(m, files));
}

function readJson(files: Files, path: string): { found: boolean; value?: unknown } {
  const raw = files(path);
  if (raw === undefined) return { found: false };
  try {
    return { found: true, value: JSON.parse(typeof raw === "string" ? raw : new TextDecoder().decode(raw)) };
  } catch {
    return { found: true };
  }
}

function packageRules(m: Obj, files: Files): ManifestError[] {
  const errs: ManifestError[] = [];
  const push = (code: string, path: string, detail = "") => errs.push({ code, path, detail });
  const strs = m.strings as Obj;
  for (const lang of ["en", "ar"]) {
    const path = `i18n/${lang}.json`;
    const r = readJson(files, path);
    if (!r.found) {
      push(CODES.missingFile, "strings", path);
      continue;
    }
    if (typeOf(r.value) !== "object") {
      push(CODES.missingString, "strings", lang);
      continue;
    }
    const words = r.value as Obj;
    for (const [key, most] of [["name", MAX_NAME], ["summary", MAX_SUMMARY]] as const) {
      const w = words[strs[key] as string];
      if (typeof w !== "string" || w.trim() === "") push(CODES.missingString, `strings/${key}`, lang);
      else if ([...w].length > most) push(CODES.stringTooLong, `strings/${key}`, lang);
    }
  }
  let props: Obj = {};
  const settings = m.settings as Obj | undefined;
  if (settings) {
    const path = settings.schema as string;
    const r = readJson(files, path);
    if (!r.found) push(CODES.missingFile, "settings/schema", path);
    else if (typeOf(r.value) !== "object") push(CODES.badSettings, "settings/schema", path);
    else {
      const p = (r.value as Obj).properties;
      props = typeOf(p) === "object" ? (p as Obj) : {};
    }
  }
  (m.schedule as Obj[]).forEach((s, i) => {
    const field = s.every_minutes_setting as string;
    const path = `schedule/${i}/every_minutes_setting`;
    if (!settings) {
      push(CODES.badSchedule, path, "no settings");
      return;
    }
    const p = props[field];
    if (typeOf(p) !== "object") {
      push(CODES.badSchedule, path, field);
      return;
    }
    const low = (p as Obj).minimum;
    if ((p as Obj).type !== "integer" || typeof low !== "number" || low < 15) push(CODES.badSchedule, path, field);
  });
  return errs;
}
