// What the store publishes on plugins-cdn.maktaba.tv (stories 54.14, 54.15 §8): countersigned
// packages, content-addressed and never overwritten, and the signed lists
// (catalog-<api major>.json, revoked.json, revoked-head.json) that the CatalogWriter
// regenerates on every change and on a timer. store-keys.json is the owner's, signed offline
// with a root key, and uploaded beside them; the store only checks that its own key is in it.
import { Root, StoreKeys, emptyState } from "../signing/verify";
import { Signer, capabilitiesSHA256, filesSHA256, sha256Hex, signEnvelope, signFile } from "../signing/sig";
import { parseJSON } from "../signing/canonical";
import committedRoot from "../../shared/plugins/plugins-root.json";
import { type Env, iso, now } from "./env";
import { inspect, repack } from "./wasm";

export const LIST_CACHE = "public, max-age=60";
export const PACKAGE_CACHE = "public, max-age=31536000, immutable";

export class PublishError extends Error {
  constructor(
    readonly code: string,
    msg: string,
  ) {
    super(`${code}: ${msg}`);
  }
}

/** The online key S (a Worker secret), checked against the store-keys.json the CDN serves. */
export async function storeSigner(env: Env): Promise<{ signer: Signer; keys: StoreKeys }> {
  if (!env.STORE_SIGNING_KEY) throw new PublishError("store_key_missing", "STORE_SIGNING_KEY is not set");
  const signer = await Signer.fromSecret(env.STORE_SIGNING_KEY);
  const obj = await env.BUCKET.get("store-keys.json");
  if (!obj) throw new PublishError("store_keys_missing", "store-keys.json is not on the CDN");
  // the same root every home embeds (shared/plugins/plugins-root.json)
  const root = await Root.parse(env.PLUGINS_ROOT ?? JSON.stringify(committedRoot));
  const keys = await root.verifyStoreKeys(await obj.text(), emptyState(), now());
  if (!keys.has(signer.id))
    throw new PublishError("store_key_unlisted", `the online key ${signer.id} is not in store-keys.json version ${keys.version}`);
  return { signer, keys };
}

interface Manifest {
  id: string;
  version: string;
  maktaba: string;
  plugin_api: string;
  platforms: string[];
  capabilities: { required: string[]; optional: string[] };
}

/**
 * Countersigns a package (54.14 §2): the developer's signature is checked again with the key
 * registered for the account, store.sig is made with the review level, the package is
 * re-packed with it, checked as a home will, and put on the CDN by its sha256 (never
 * overwritten). The version's row records it.
 */
async function countersignPackage(
  env: Env,
  pkg: Uint8Array,
  plugin: string,
  version: string,
  account: string,
  level: "automated" | "reviewed",
): Promise<{ sha256: string; size: number }> {
  const i = await inspect(pkg);
  const m = JSON.parse(i.manifest) as Manifest;
  if (m.id !== plugin || m.version !== version) throw new PublishError("package_mismatch", "the package is another plugin or version");
  if (!i.developer_sig) throw new PublishError("developer_sig_missing", "the package has no developer.sig");
  const dev = parseJSON(i.developer_sig) as { key_id: string };
  const key = await env.DB_CATALOG.prepare("SELECT revoked_at, suspended_at FROM developer_keys WHERE id = ? AND account = ?")
    .bind(dev.key_id, account)
    .first<{ revoked_at: string | null; suspended_at: string | null }>();
  if (!key || key.revoked_at || key.suspended_at) throw new PublishError("developer_key_inactive", dev.key_id);
  const { signer, keys } = await storeSigner(env);
  const caps = await capabilitiesSHA256(m.capabilities);
  // the developer's signature, checked by the home's rules before the store vouches for it
  await keys.verifyPackage({
    filesJSON: i.files_json,
    developerSig: i.developer_sig,
    pluginId: m.id,
    version: m.version,
    capabilitiesSHA256: caps,
  });
  const at = iso(now());
  const storeSig = await signFile("store", signer, {
    plugin_id: m.id,
    version: m.version,
    files_sha256: await filesSHA256(i.files_json),
    developer_key_id: dev.key_id,
    review_level: level,
    reviewed_at: at,
    capabilities_sha256: caps,
  });
  const out = await repack(pkg, storeSig);
  // the store checks its own work as a home will, store.sig included
  const back = await inspect(out);
  const verified = await keys.verifyPackage({
    filesJSON: back.files_json,
    developerSig: back.developer_sig,
    storeSig: back.store_sig,
    pluginId: m.id,
    version: m.version,
    capabilitiesSHA256: caps,
  });
  if (verified.level !== level) throw new PublishError("countersign_failed", "the re-packed file does not verify");
  const sha = await sha256Hex(out);
  if (!(await env.BUCKET.head(`${sha}.mkplugin`))) {
    await env.BUCKET.put(`${sha}.mkplugin`, out, {
      httpMetadata: { contentType: "application/vnd.maktaba.plugin", cacheControl: PACKAGE_CACHE },
    });
  }
  await env.DB_CATALOG.prepare(
    `UPDATE versions SET sha256 = ?, size = ?, files_sha256 = ?, manifest = ?, capabilities = ?, maktaba = ?, plugin_api = ?,
       platforms = ?, developer_key = ?, review_level = ?, status = 'approved', store_sig = ?, store_key = ?, released = COALESCE(released, ?)
     WHERE plugin = ? AND version = ?`,
  )
    .bind(
      sha,
      out.length,
      verified.filesSHA256,
      i.manifest,
      JSON.stringify([...m.capabilities.required, ...m.capabilities.optional]),
      m.maktaba,
      m.plugin_api,
      JSON.stringify(m.platforms),
      dev.key_id,
      level,
      storeSig,
      signer.id,
      at,
      m.id,
      m.version,
    )
    .run();
  return { sha256: sha, size: out.length };
}

/** Countersigns a submission's package and lists its plugin (a reviewer's or the checks' approval). */
export async function countersign(
  env: Env,
  submissionId: string,
  level: "automated" | "reviewed",
): Promise<{ sha256: string; size: number }> {
  const s = await env.DB_CATALOG.prepare("SELECT plugin, version, package_key, uploaded_by FROM submissions WHERE id = ?")
    .bind(submissionId)
    .first<{ plugin: string; version: string; package_key: string; uploaded_by: string }>();
  if (!s) throw new PublishError("not_found", submissionId);
  const obj = await env.PRIVATE.get(s.package_key);
  if (!obj) throw new PublishError("package_missing", s.package_key);
  const out = await countersignPackage(env, new Uint8Array(await obj.arrayBuffer()), s.plugin, s.version, s.uploaded_by, level);
  await env.DB_CATALOG.prepare("UPDATE plugins SET status = 'listed' WHERE id = ? AND status = 'draft'").bind(s.plugin).run();
  return out;
}

/**
 * The batch re-sign of a key rotation (54.14 §5): every approved version not yet signed by
 * the current key, a page at a time; answers the cursor of the next page ("" when done).
 * The old file stays on the CDN (content-addressed); the catalog then names the new one.
 */
export async function resignPage(env: Env, after: string, page = 20): Promise<string> {
  const { signer } = await storeSigner(env);
  const rows = await env.DB_CATALOG.prepare(
    `SELECT v.plugin, v.version, v.sha256, v.review_level, p.developer FROM versions v JOIN plugins p ON p.id = v.plugin
     WHERE v.status = 'approved' AND v.store_key <> ? AND (v.plugin || '@' || v.version) > ? ORDER BY v.plugin, v.version LIMIT ?`,
  )
    .bind(signer.id, after, page)
    .all<{ plugin: string; version: string; sha256: string; review_level: "automated" | "reviewed"; developer: string }>();
  for (const r of rows.results) {
    const obj = await env.BUCKET.get(`${r.sha256}.mkplugin`);
    if (!obj) continue;
    try {
      await countersignPackage(env, new Uint8Array(await obj.arrayBuffer()), r.plugin, r.version, r.developer, r.review_level);
    } catch (e) {
      // one version that cannot be re-signed (its developer key revoked since) does not stop
      // the batch; a reviewer decides it
      console.error(JSON.stringify({ event: "resign_failed", plugin: r.plugin, version: r.version, error: String(e) }));
    }
  }
  const last = rows.results.at(-1);
  return rows.results.length === page && last ? `${last.plugin}@${last.version}` : "";
}

function roundInstalls(n: number): number {
  if (n < 10) return 0;
  if (n < 100) return Math.floor(n / 10) * 10;
  const p = 10 ** (Math.floor(Math.log10(n)) - 1);
  return Math.floor(n / p) * p;
}

/**
 * The next version of a list: above both the database's count and the copy on the CDN, so a
 * database restored to an earlier time never publishes a version homes refuse as a rollback.
 */
async function nextVersion(env: Env, name: string, file: string): Promise<number> {
  const r = await env.DB_CATALOG.prepare("SELECT version FROM lists WHERE name = ?").bind(name).first<{ version: number }>();
  let published = 0;
  const obj = await env.BUCKET.get(file);
  if (obj) {
    try {
      published = Number((JSON.parse(await obj.text()) as { signed?: { version?: number } }).signed?.version ?? 0);
    } catch {
      published = 0;
    }
  }
  return Math.max(r?.version ?? 0, published) + 1;
}

async function put(env: Env, name: string, text: string, version: number, issued: string): Promise<string> {
  const sha = await sha256Hex(text);
  await env.BUCKET.put(name, text, { httpMetadata: { contentType: "application/json", cacheControl: LIST_CACHE } });
  await env.DB_CATALOG.prepare(
    "INSERT INTO lists (name, version, sha256, issued) VALUES (?, ?, ?, ?) ON CONFLICT (name) DO UPDATE SET version = ?, sha256 = ?, issued = ?",
  )
    .bind(name, version, sha, issued, version, sha, issued)
    .run();
  return sha;
}

async function purge(env: Env, names: string[]): Promise<void> {
  if (!env.CF_ZONE_ID || !env.CF_PURGE_TOKEN) return;
  await fetch(`https://api.cloudflare.com/client/v4/zones/${env.CF_ZONE_ID}/purge_cache`, {
    method: "POST",
    headers: { Authorization: `Bearer ${env.CF_PURGE_TOKEN}`, "Content-Type": "application/json" },
    body: JSON.stringify({ files: names.map((n) => `${env.CDN_ORIGIN}/${n}`) }),
  });
}

interface VersionRow {
  plugin: string;
  version: string;
  sha256: string;
  size: number;
  files_sha256: string;
  capabilities: string;
  maktaba: string;
  plugin_api: string;
  platforms: string;
  review_level: string;
  released: string;
}

interface PluginRow {
  id: string;
  developer: string;
  developer_name: string;
  names: string;
  summaries: string;
  descriptions: string;
  category: string;
  tags: string;
  icon: string;
  machine_translated: string;
}

/** The catalog of plugin API major (only approved versions of listed plugins). */
export async function buildCatalog(env: Env, apiMajor: number, version: number, at: Date): Promise<Record<string, unknown>> {
  const plugins = await env.DB_CATALOG.prepare("SELECT * FROM plugins WHERE status = 'listed' ORDER BY id").all<PluginRow>();
  const versions = await env.DB_CATALOG.prepare(
    "SELECT v.* FROM versions v JOIN plugins p ON p.id = v.plugin WHERE p.status = 'listed' AND v.status = 'approved' ORDER BY v.plugin, v.released DESC",
  ).all<VersionRow>();
  const installs = await env.DB_INSTALLS.prepare("SELECT plugin, COUNT(*) AS n FROM installs GROUP BY plugin").all<{
    plugin: string;
    n: number;
  }>();
  const count = new Map(installs.results.map((r) => [r.plugin, r.n]));
  const out = [];
  for (const p of plugins.results) {
    const vs = versions.results
      .filter((v) => v.plugin === p.id && Number(v.plugin_api.split(".")[0]) === apiMajor)
      .map((v) => ({
        version: v.version,
        sha256: v.sha256,
        size: v.size,
        url: `${env.CDN_ORIGIN}/${v.sha256}.mkplugin`,
        maktaba: v.maktaba,
        plugin_api: v.plugin_api,
        platforms: JSON.parse(v.platforms) as string[],
        capabilities: JSON.parse(v.capabilities) as string[],
        review_level: v.review_level,
        released: v.released,
        files_sha256: v.files_sha256,
      }));
    if (vs.length === 0) continue;
    out.push({
      id: p.id,
      developer: p.developer_name,
      names: JSON.parse(p.names),
      summaries: JSON.parse(p.summaries),
      icon: p.icon,
      category: p.category,
      tags: JSON.parse(p.tags),
      rating_avg_x100: 0,
      rating_count: 0,
      installs: roundInstalls(count.get(p.id) ?? 0),
      price: "free",
      machine_translated: JSON.parse(p.machine_translated),
      versions: vs,
      extra: { descriptions: JSON.parse(p.descriptions) },
    });
  }
  return { version, issued: iso(at), expires: iso(new Date(at.getTime() + 7 * 24 * 3600_000)), api_major: apiMajor, plugins: out };
}

/** Regenerates and signs the catalog, the revocation list and its head; writes them to R2. */
export async function regenerate(env: Env): Promise<{ catalog: number; revoked: number }> {
  const { signer } = await storeSigner(env);
  const at = now();
  const issued = iso(at);
  const catalogVersion = await nextVersion(env, "catalog-1", "catalog-1.json");
  const catalog = await signEnvelope("catalog", await buildCatalog(env, 1, catalogVersion, at), signer);
  await put(env, "catalog-1.json", catalog, catalogVersion, issued);
  const rows = await env.DB_CATALOG.prepare("SELECT * FROM revocations WHERE lifted_at IS NULL ORDER BY id").all<{
    plugin_id: string;
    versions: string;
    developer_key_id: string;
    action: string;
    reason_code: string;
    issued: string;
  }>();
  const entries = rows.results.map((r) => ({
    plugin_id: r.plugin_id,
    versions: r.versions,
    ...(r.developer_key_id ? { developer_key_id: r.developer_key_id } : {}),
    action: r.action,
    reason_code: r.reason_code,
    issued: r.issued,
  }));
  const revokedVersion = await nextVersion(env, "revoked", "revoked.json");
  const revoked = await signEnvelope(
    "revoked",
    { version: revokedVersion, issued, expires: iso(new Date(at.getTime() + 48 * 3600_000)), entries },
    signer,
  );
  const sha = await put(env, "revoked.json", revoked, revokedVersion, issued);
  // the head after the list, so a home that sees the head finds the list
  const head = await signEnvelope("revoked-head", { version: revokedVersion, issued, sha256: sha }, signer);
  await put(env, "revoked-head.json", head, revokedVersion, issued);
  await purge(env, ["catalog-1.json", "revoked.json", "revoked-head.json"]);
  return { catalog: catalogVersion, revoked: revokedVersion };
}
