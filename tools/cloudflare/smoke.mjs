// Smoke checks after a Cloudflare deploy (Epic 50, story 50.34 §3). Each check retries for
// up to 5 minutes (a first deploy's certificate or route takes time) and the run fails on the
// first check that never passes.
//
//   node smoke.mjs cloud-web|admin-web|api|zone|website|website-preview [--base <url>]
//                  [--attempts 30] [--wait 10]
//
// SMOKE_BASE_<HOST> (for example SMOKE_BASE_API=http://127.0.0.1:9000) points a host at a local
// server in the tests; the default is https://<host>. A check over plain http reads
// SMOKE_BASE_<HOST>_HTTP. --base <url> points the target's own host (the host of its first
// check) at <url>: a version's preview URL, or a local `wrangler dev`.
//
// A check is { host, path, status, marker, headers } (marker: a string or a list the body must
// contain), or { host, path, notMarker }: the host must not answer with the marker, and passes
// on any other answer or on no answer at all (a name that does not resolve).

/** The checks of each target: a URL, the status it must answer, and what else must hold. */
export const TARGETS = {
  "cloud-web": [
    { host: "app.maktaba.tv", path: "/", status: 200, marker: 'name="maktaba-app" content="user"', headers: { "content-security-policy": /default-src/ } },
    { host: "app.maktaba.tv", path: "/v1/admin/accounts", status: 404 },
  ],
  "admin-web": [
    {
      host: "admin.maktaba.tv",
      path: "/",
      status: 200,
      marker: 'name="maktaba-app" content="admin"',
      headers: { "x-robots-tag": /noindex/, "content-security-policy": /default-src/ },
    },
    { host: "admin.maktaba.tv", path: "/v1/admin/accounts", status: 401 },
  ],
  api: [{ host: "api.maktaba.tv", path: "/readyz", status: 200 }],
  // the edge answers an admin path on a user name itself (50.30 §2 rule 1, §5's JSON)
  zone: [{ host: "api.maktaba.tv", path: "/v1/admin/accounts", status: 404, marker: '"error":"not_found"' }],
};

// maktaba.tv, the website (Epic 60, story 60.03). The checks a version's preview URL can
// answer; the install chooser (60.05), the privacy page (60.07, with the relay's policy in it)
// and a problem page and the index (60.06: not-found, a slug the registry has, in both
// languages) are here.
const WEBSITE = "maktaba.tv";
const SITE = '<meta name="maktaba-site" content="maktaba-website" />';
const WEBSITE_PAGES = [
  {
    host: WEBSITE,
    path: "/",
    status: 200,
    marker: ['<html lang="en" dir="ltr">', 'id="lang-switch" class="lang-switch" href="/ar/"', SITE],
    headers: {
      // HSTS without includeSubDomains: the relay's names are DNS-only (README §6.2)
      "strict-transport-security": /^max-age=\d+$/,
      "content-security-policy": /default-src 'self'.*frame-ancestors 'none'/,
    },
  },
  { host: WEBSITE, path: "/ar/", status: 200, marker: ['<html lang="ar" dir="rtl">', SITE] },
  { host: WEBSITE, path: "/install/docker/", status: 200, marker: '<main id="app">' },
  { host: WEBSITE, path: "/install/", status: 200, marker: ['href="/install/nas/"', SITE] },
  { host: WEBSITE, path: "/legal/privacy/", status: 200, marker: ['data-legal="privacy"', 'data-policy="CurrentPolicy"', SITE] },
  { host: WEBSITE, path: "/problems/not-found/", status: 200, marker: ['data-problem="not-found"', "https://maktaba.tv/problems/not-found", SITE] },
  { host: WEBSITE, path: "/ar/problems/not-found/", status: 200, marker: ['<html lang="ar" dir="rtl">', 'data-problem="not-found"', SITE] },
  { host: WEBSITE, path: "/problems/", status: 200, marker: ['href="/problems/not-found/"', SITE] },
  { host: WEBSITE, path: "/this-does-not-exist", status: 404, marker: ['content="404"', SITE] },
  { host: WEBSITE, path: "/ar/this-does-not-exist", status: 404, marker: ['<html lang="ar" dir="rtl">', 'content="404"'] },
  { host: WEBSITE, path: "/schema/log-line.json", status: 200, marker: '"$id": "https://maktaba.tv/schema/log-line.json"' },
  { host: WEBSITE, path: "/.well-known/security.txt", status: 200, marker: ["Expires: ", "Canonical: https://maktaba.tv/"] },
];
TARGETS["website-preview"] = WEBSITE_PAGES;
// live: also the canonical redirects, and no other name on the zone answering from this Worker
TARGETS.website = [
  ...WEBSITE_PAGES,
  { host: `www.${WEBSITE}`, path: "/install/", status: 301, headers: { location: /^https:\/\/maktaba\.tv\/install\/$/ } },
  { host: WEBSITE, scheme: "http", path: "/", status: 301, headers: { location: /^https:\/\/maktaba\.tv\/$/ } },
  { host: "app.maktaba.tv", path: "/", notMarker: "maktaba-website" },
];

function baseFor(c, env) {
  const http = c.scheme === "http";
  const key = `SMOKE_BASE_${c.host.split(".")[0].toUpperCase().replace(/-/g, "_")}${http ? "_HTTP" : ""}`;
  return env[key] || `${http ? "http" : "https"}://${c.host}`;
}

const markers = (m) => (m === undefined ? [] : Array.isArray(m) ? m : [m]);

/** One check once: answers "" when it holds, else what failed. */
export async function once(c, env = process.env, fetchImpl = fetch) {
  const url = `${baseFor(c, env)}${c.path}`;
  let res;
  try {
    res = await fetchImpl(url, { redirect: "manual" });
  } catch (e) {
    // a name that answers nothing cannot be answering from the Worker
    if (c.notMarker) return "";
    return `${url}: ${e.cause?.code ?? e.message}`;
  }
  const body = await res.text();
  if (c.notMarker) return body.includes(c.notMarker) ? `${url}: answers with ${c.notMarker}` : "";
  if (res.status !== c.status) return `${url}: status ${res.status}, want ${c.status}`;
  for (const m of markers(c.marker)) if (!body.includes(m)) return `${url}: no ${m}`;
  for (const [h, re] of Object.entries(c.headers ?? {})) {
    const v = res.headers.get(h);
    if (!v || !re.test(v)) return `${url}: header ${h} ${v ? `is ${v}` : "missing"}`;
  }
  return "";
}

/** A target's checks, each retried; answers the failures (empty: all passed). */
export async function run(target, { attempts = 30, waitMs = 10_000, env = process.env, fetchImpl = fetch, log = console.log, base = "" } = {}) {
  const checks = TARGETS[target];
  if (!checks) throw new Error(`unknown target ${target} (known: ${Object.keys(TARGETS).join(", ")})`);
  if (base) {
    // --base: the target's own host (its first check's) answers from <base>
    const own = checks[0].host;
    env = { ...env, [`SMOKE_BASE_${own.split(".")[0].toUpperCase().replace(/-/g, "_")}`]: base.replace(/\/$/, "") };
  }
  const failures = [];
  for (const c of checks) {
    let why = "";
    for (let i = 1; i <= attempts; i++) {
      why = await once(c, env, fetchImpl);
      if (!why) break;
      if (i < attempts) {
        log(`waiting (${i}/${attempts}): ${why}`);
        await new Promise((r) => setTimeout(r, waitMs));
      }
    }
    if (why) failures.push(why);
    else log(`ok: ${baseFor(c, env)}${c.path} (${c.notMarker ? `not ${c.notMarker}` : c.status})`);
  }
  return failures;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const args = process.argv.slice(2);
  const str = (k, d) => {
    const i = args.indexOf(k);
    return i >= 0 ? args[i + 1] : d;
  };
  const opt = (k, d) => Number(str(k, d));
  const failures = await run(args[0], { attempts: opt("--attempts", 30), waitMs: opt("--wait", 10) * 1000, base: str("--base", "") });
  for (const f of failures) console.log(`::error title=Smoke check::${f}`);
  process.exit(failures.length ? 1 : 0);
}
