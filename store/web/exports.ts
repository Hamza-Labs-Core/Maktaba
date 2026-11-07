// The nightly export (story 54.15 §9): each D1 database as SQL (one INSERT per row, after the
// migrations' schema), gzipped and encrypted for the owner, written to the private bucket as
// exports/<db>/<date>.sql.gz.enc (kept 90 days by an R2 lifecycle rule). The owner holds the
// RSA private key outside Cloudflare; the Worker has only the public half
// (EXPORT_PUBLIC_KEY, SPKI in base64). Signing keys are never in an export.
//
// File: "MKX1" ‖ u32 header length ‖ header JSON {wrapped_key, iv} ‖ AES-256-GCM ciphertext
// of the gzipped SQL. The AES key is fresh per file, wrapped with RSA-OAEP (SHA-256).
import { b64url, fromB64url } from "./crypto";
import { type Env, now } from "./env";

const TABLES: Record<string, { binding: "DB_ACCOUNTS" | "DB_CATALOG" | "DB_INSTALLS"; tables: string[] }> = {
  "store-accounts": {
    binding: "DB_ACCOUNTS",
    tables: ["accounts", "account_mfa", "recovery_codes", "email_tokens", "roles", "mfa_resets", "bootstrap_used", "audit"],
  },
  "store-catalog": {
    binding: "DB_CATALOG",
    tables: [
      "developers",
      "developer_prefixes",
      "developer_keys",
      "plugins",
      "versions",
      "submissions",
      "revocations",
      "lists",
      "flagged_words",
    ],
  },
  "store-installs": { binding: "DB_INSTALLS", tables: ["homes", "installs", "install_counts"] },
};

function literal(v: unknown): string {
  if (v === null || v === undefined) return "NULL";
  if (typeof v === "number") return Number.isFinite(v) ? String(v) : "NULL";
  if (typeof v === "boolean") return v ? "1" : "0";
  return `'${String(v).replace(/'/g, "''")}'`;
}

/** One database as SQL INSERTs (sessions and passkey challenges are left out: short-lived). */
export async function dump(env: Env, db: string): Promise<string> {
  const spec = TABLES[db];
  if (!spec) throw new Error(`no database ${db}`);
  const d = env[spec.binding];
  const out: string[] = [`-- ${db} at ${now().toISOString()}`];
  for (const t of spec.tables) {
    let offset = 0;
    for (;;) {
      const rows = await d.prepare(`SELECT * FROM ${t} LIMIT 500 OFFSET ${offset}`).all<Record<string, unknown>>();
      for (const r of rows.results) {
        const cols = Object.keys(r);
        out.push(`INSERT INTO ${t} (${cols.join(", ")}) VALUES (${cols.map((c) => literal(r[c])).join(", ")});`);
      }
      if (rows.results.length < 500) break;
      offset += 500;
    }
  }
  return out.join("\n") + "\n";
}

async function gzip(b: Uint8Array): Promise<Uint8Array> {
  const s = new Response(b).body!.pipeThrough(new CompressionStream("gzip"));
  return new Uint8Array(await new Response(s).arrayBuffer());
}

async function gunzip(b: Uint8Array): Promise<Uint8Array> {
  const s = new Response(b).body!.pipeThrough(new DecompressionStream("gzip"));
  return new Uint8Array(await new Response(s).arrayBuffer());
}

export async function encrypt(publicSpkiB64: string, plain: Uint8Array): Promise<Uint8Array> {
  const pub = await crypto.subtle.importKey(
    "spki",
    fromB64url(publicSpkiB64.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")) as BufferSource,
    { name: "RSA-OAEP", hash: "SHA-256" },
    false,
    ["wrapKey"],
  );
  const key = (await crypto.subtle.generateKey({ name: "AES-GCM", length: 256 }, true, ["encrypt"])) as CryptoKey;
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const ct = new Uint8Array(
    await crypto.subtle.encrypt({ name: "AES-GCM", iv: iv as BufferSource }, key, (await gzip(plain)) as BufferSource),
  );
  const wrapped = new Uint8Array(await crypto.subtle.wrapKey("raw", key, pub, { name: "RSA-OAEP" }));
  const header = new TextEncoder().encode(JSON.stringify({ wrapped_key: b64url(wrapped), iv: b64url(iv) }));
  const out = new Uint8Array(8 + header.length + ct.length);
  out.set(new TextEncoder().encode("MKX1"), 0);
  new DataView(out.buffer).setUint32(4, header.length);
  out.set(header, 8);
  out.set(ct, 8 + header.length);
  return out;
}

/** The owner's side (scripts/decrypt-export.ts and the restore drill): the SQL back. */
export async function decrypt(privatePkcs8: Uint8Array, file: Uint8Array): Promise<string> {
  if (new TextDecoder().decode(file.subarray(0, 4)) !== "MKX1") throw new Error("not a store export");
  const n = new DataView(file.buffer, file.byteOffset).getUint32(4);
  const h = JSON.parse(new TextDecoder().decode(file.subarray(8, 8 + n))) as { wrapped_key: string; iv: string };
  const priv = await crypto.subtle.importKey("pkcs8", privatePkcs8 as BufferSource, { name: "RSA-OAEP", hash: "SHA-256" }, false, [
    "unwrapKey",
  ]);
  const key = await crypto.subtle.unwrapKey(
    "raw",
    fromB64url(h.wrapped_key) as BufferSource,
    priv,
    { name: "RSA-OAEP" },
    { name: "AES-GCM" },
    false,
    ["decrypt"],
  );
  const plain = new Uint8Array(
    await crypto.subtle.decrypt({ name: "AES-GCM", iv: fromB64url(h.iv) as BufferSource }, key, file.subarray(8 + n) as BufferSource),
  );
  return new TextDecoder().decode(await gunzip(plain));
}

/** The nightly Cron Trigger: every database to the private bucket. */
export async function nightlyExport(env: Env): Promise<string[]> {
  if (!env.EXPORT_PUBLIC_KEY) return [];
  const date = now().toISOString().slice(0, 10);
  const written: string[] = [];
  for (const db of Object.keys(TABLES)) {
    const key = `exports/${db}/${date}.sql.gz.enc`;
    await env.PRIVATE.put(key, await encrypt(env.EXPORT_PUBLIC_KEY, new TextEncoder().encode(await dump(env, db))));
    written.push(key);
  }
  return written;
}
