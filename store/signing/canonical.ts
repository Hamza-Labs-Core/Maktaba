// Canonical JSON (story 54.14 §2): the bytes every signature covers, the same as Go's
// shared/plugins/pluginsig/canonical.go and Rust's plugind/crates/sig, checked on
// shared/plugins/testdata/signatures/canonical.json.
//
// Objects: keys sorted by their UTF-8 bytes, no key twice, no spaces. Strings: written as
// they are except `"`, `\` and the control characters below U+0020 (\b \f \n \r \t, the
// rest \u00xx in lowercase hex). Numbers: integers within ±(2^53 − 1) only. A \u escape of
// a surrogate is refused (the three languages' readers disagree on it), and so is text
// that is not UTF-8.

export type JSONValue = null | boolean | number | string | JSONValue[] | { [key: string]: JSONValue };

export class CanonicalError extends Error {
  constructor(msg: string) {
    super(`not canonical JSON: ${msg}`);
  }
}

const enc = new TextEncoder();
const dec = new TextDecoder("utf-8", { fatal: true, ignoreBOM: false });

/** Reads one JSON value strictly (no key twice, integers only, nothing after it). */
export function parseJSON(input: string | Uint8Array): JSONValue {
  let text: string;
  if (typeof input === "string") {
    text = input;
  } else {
    try {
      text = dec.decode(input);
    } catch {
      throw new CanonicalError("the text is not UTF-8");
    }
  }
  let i = 0;
  const ws = () => {
    while (i < text.length && (text[i] === " " || text[i] === "\t" || text[i] === "\n" || text[i] === "\r")) i++;
  };
  const value = (): JSONValue => {
    ws();
    const c = text[i];
    if (c === "{") {
      i++;
      const obj: { [key: string]: JSONValue } = Object.create(null);
      ws();
      if (text[i] === "}") {
        i++;
        return { ...obj };
      }
      for (;;) {
        ws();
        if (text[i] !== '"') throw new CanonicalError("a key is not a string");
        const k = str();
        if (Object.prototype.hasOwnProperty.call(obj, k)) throw new CanonicalError(`the key ${JSON.stringify(k)} comes twice`);
        ws();
        if (text[i] !== ":") throw new CanonicalError("expected ':'");
        i++;
        obj[k] = value();
        ws();
        if (text[i] === ",") {
          i++;
          continue;
        }
        if (text[i] === "}") {
          i++;
          return { ...obj };
        }
        throw new CanonicalError("expected ',' or '}'");
      }
    }
    if (c === "[") {
      i++;
      const arr: JSONValue[] = [];
      ws();
      if (text[i] === "]") {
        i++;
        return arr;
      }
      for (;;) {
        arr.push(value());
        ws();
        if (text[i] === ",") {
          i++;
          continue;
        }
        if (text[i] === "]") {
          i++;
          return arr;
        }
        throw new CanonicalError("expected ',' or ']'");
      }
    }
    if (c === '"') return str();
    if (text.startsWith("true", i)) {
      i += 4;
      return true;
    }
    if (text.startsWith("false", i)) {
      i += 5;
      return false;
    }
    if (text.startsWith("null", i)) {
      i += 4;
      return null;
    }
    const m = /^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?/.exec(text.slice(i));
    if (!m) throw new CanonicalError(`unexpected ${JSON.stringify(text.slice(i, i + 8))}`);
    i += m[0].length;
    return checkInt(m[0]);
  };
  const str = (): string => {
    i++; // the opening quote
    let out = "";
    for (;;) {
      if (i >= text.length) throw new CanonicalError("an unterminated string");
      const c = text[i]!;
      if (c === '"') {
        i++;
        return out;
      }
      if (c < " ") throw new CanonicalError("a control character in a string");
      if (c !== "\\") {
        out += c;
        i++;
        continue;
      }
      const e = text[i + 1];
      i += 2;
      switch (e) {
        case '"':
          out += '"';
          break;
        case "\\":
          out += "\\";
          break;
        case "/":
          out += "/";
          break;
        case "b":
          out += "\b";
          break;
        case "f":
          out += "\f";
          break;
        case "n":
          out += "\n";
          break;
        case "r":
          out += "\r";
          break;
        case "t":
          out += "\t";
          break;
        case "u": {
          const hex = text.slice(i, i + 4);
          if (!/^[0-9a-fA-F]{4}$/.test(hex)) throw new CanonicalError("a bad \\u escape");
          const n = parseInt(hex, 16);
          if (n >= 0xd800 && n <= 0xdfff) throw new CanonicalError("a surrogate escape");
          out += String.fromCharCode(n);
          i += 4;
          break;
        }
        default:
          throw new CanonicalError("a bad escape");
      }
    }
  };
  const v = value();
  ws();
  if (i !== text.length) throw new CanonicalError("data after the value");
  return v;
}

function checkInt(s: string): number {
  if (!/^-?(0|[1-9][0-9]*)$/.test(s) || s === "-0") throw new CanonicalError(`${s} is not an integer`);
  const n = Number(s);
  if (!Number.isSafeInteger(n)) throw new CanonicalError(`${s} is out of range`);
  return n;
}

function compareUTF8(a: string, b: string): number {
  const x = enc.encode(a);
  const y = enc.encode(b);
  const n = Math.min(x.length, y.length);
  for (let i = 0; i < n; i++) {
    if (x[i] !== y[i]) return x[i]! - y[i]!;
  }
  return x.length - y.length;
}

function writeString(s: string): string {
  let out = '"';
  for (let i = 0; i < s.length; i++) {
    const code = s.charCodeAt(i);
    if (code >= 0xd800 && code <= 0xdbff) {
      const next = s.charCodeAt(i + 1);
      if (!(next >= 0xdc00 && next <= 0xdfff)) throw new CanonicalError("a string is not UTF-8");
      out += s[i]! + s[i + 1]!;
      i++;
      continue;
    }
    if (code >= 0xdc00 && code <= 0xdfff) throw new CanonicalError("a string is not UTF-8");
    const c = s[i]!;
    switch (c) {
      case '"':
        out += '\\"';
        break;
      case "\\":
        out += "\\\\";
        break;
      case "\b":
        out += "\\b";
        break;
      case "\f":
        out += "\\f";
        break;
      case "\n":
        out += "\\n";
        break;
      case "\r":
        out += "\\r";
        break;
      case "\t":
        out += "\\t";
        break;
      default:
        out += code < 0x20 ? "\\u" + code.toString(16).padStart(4, "0") : c;
    }
  }
  return out + '"';
}

function write(v: unknown): string {
  if (v === null) return "null";
  if (v === true) return "true";
  if (v === false) return "false";
  if (typeof v === "number") {
    if (!Number.isSafeInteger(v) || Object.is(v, -0)) throw new CanonicalError(`${v} is not an integer`);
    return String(v);
  }
  if (typeof v === "string") return writeString(v);
  if (Array.isArray(v)) return "[" + v.map(write).join(",") + "]";
  if (typeof v === "object") {
    const o = v as Record<string, unknown>;
    const keys = Object.keys(o).filter((k) => o[k] !== undefined);
    keys.sort(compareUTF8);
    return "{" + keys.map((k) => writeString(k) + ":" + write(o[k])).join(",") + "}";
  }
  throw new CanonicalError(`a ${typeof v}`);
}

/** The canonical text of a JS value (undefined fields are left out). */
export function canonicalStringOf(v: unknown): string {
  return write(v);
}

/** The canonical bytes of a JS value. */
export function canonicalOf(v: unknown): Uint8Array {
  return enc.encode(write(v));
}

/** The canonical bytes of a JSON text. */
export function canonical(text: string | Uint8Array): Uint8Array {
  return canonicalOf(parseJSON(text));
}
