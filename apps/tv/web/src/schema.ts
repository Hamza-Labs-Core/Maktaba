// Story 58.09 T06 — checks a bridge message against bridge.schema.json. The schema uses a
// small part of JSON Schema (type, const, enum, required, properties, additionalProperties,
// items, $ref into definitions), so the check is here, in both sides' tests, with no
// dependency.

import schema from "../bridge.schema.json";

type Node = Record<string, unknown>;
export type Direction = "uiToLauncher" | "launcherToUi";

function typeOk(v: unknown, t: string): boolean {
  switch (t) {
    case "object":
      return !!v && typeof v === "object" && !Array.isArray(v);
    case "array":
      return Array.isArray(v);
    case "string":
      return typeof v === "string";
    case "number":
      return typeof v === "number" && Number.isFinite(v);
    case "integer":
      return typeof v === "number" && Number.isInteger(v);
    case "boolean":
      return typeof v === "boolean";
    case "null":
      return v === null;
    default:
      return false;
  }
}

function resolve(node: Node): Node {
  const ref = node.$ref;
  if (typeof ref !== "string") return node;
  const name = ref.replace("#/definitions/", "");
  return (schema.definitions as Record<string, Node>)[name]!;
}

/** The problems of v against node, each naming its path ("" when there are none). */
export function check(v: unknown, raw: Node, path = "$"): string[] {
  const node = resolve(raw);
  const out: string[] = [];
  if ("const" in node && v !== node.const) out.push(`${path}: is not ${JSON.stringify(node.const)}`);
  if (Array.isArray(node.enum) && !(node.enum as unknown[]).includes(v))
    out.push(`${path}: ${JSON.stringify(v)} is not one of ${JSON.stringify(node.enum)}`);
  if (node.type !== undefined) {
    const types = Array.isArray(node.type) ? (node.type as string[]) : [node.type as string];
    if (!types.some((t) => typeOk(v, t))) {
      out.push(`${path}: is not ${types.join(" or ")}`);
      return out;
    }
  }
  if (typeOk(v, "object") && node.properties) {
    const obj = v as Record<string, unknown>;
    const props = node.properties as Record<string, Node>;
    for (const r of (node.required as string[] | undefined) ?? [])
      if (!(r in obj)) out.push(`${path}.${r}: is missing`);
    for (const k of Object.keys(obj)) {
      if (props[k]) out.push(...check(obj[k], props[k], `${path}.${k}`));
      else if (node.additionalProperties === false) out.push(`${path}.${k}: is not in the schema`);
    }
  }
  if (Array.isArray(v) && node.items) v.forEach((x, i) => out.push(...check(x, node.items as Node, `${path}[${i}]`)));
  return out;
}

/** The problems of one message sent in a direction. */
export function checkMessage(direction: Direction, msg: unknown): string[] {
  const type = msg && typeof msg === "object" ? (msg as { type?: unknown }).type : undefined;
  const byType = (schema as unknown as Record<Direction, Record<string, Node>>)[direction];
  if (typeof type !== "string" || !byType[type]) return [`$.type: ${String(type)} is not a ${direction} message`];
  return check(msg, byType[type]);
}

/** The message types of a direction. */
export function messageTypes(direction: Direction): string[] {
  return Object.keys((schema as unknown as Record<Direction, Record<string, Node>>)[direction]);
}
