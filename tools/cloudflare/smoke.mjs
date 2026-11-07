// Smoke checks after a Cloudflare deploy (Epic 50, story 50.34 §3). Each check retries for
// up to 5 minutes (a first deploy's certificate or route takes time) and the run fails on the
// first check that never passes.
//
//   node smoke.mjs cloud-web|admin-web|api|zone [--attempts 30] [--wait 10]
//
// SMOKE_BASE_<HOST> (for example SMOKE_BASE_API=http://127.0.0.1:9000) points a host at a local
// server in the tests; the default is https://<host>.

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

function baseFor(host, env) {
  const key = `SMOKE_BASE_${host.split(".")[0].toUpperCase().replace(/-/g, "_")}`;
  return env[key] || `https://${host}`;
}

/** One check once: answers "" when it holds, else what failed. */
export async function once(c, env = process.env, fetchImpl = fetch) {
  const url = `${baseFor(c.host, env)}${c.path}`;
  let res;
  try {
    res = await fetchImpl(url, { redirect: "manual" });
  } catch (e) {
    return `${url}: ${e.message}`;
  }
  const body = await res.text();
  if (res.status !== c.status) return `${url}: status ${res.status}, want ${c.status}`;
  if (c.marker && !body.includes(c.marker)) return `${url}: no ${c.marker}`;
  for (const [h, re] of Object.entries(c.headers ?? {})) {
    const v = res.headers.get(h);
    if (!v || !re.test(v)) return `${url}: header ${h} ${v ? `is ${v}` : "missing"}`;
  }
  return "";
}

/** A target's checks, each retried; answers the failures (empty: all passed). */
export async function run(target, { attempts = 30, waitMs = 10_000, env = process.env, fetchImpl = fetch, log = console.log } = {}) {
  const checks = TARGETS[target];
  if (!checks) throw new Error(`unknown target ${target} (known: ${Object.keys(TARGETS).join(", ")})`);
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
    else log(`ok: https://${c.host}${c.path} (${c.status})`);
  }
  return failures;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const args = process.argv.slice(2);
  const opt = (k, d) => {
    const i = args.indexOf(k);
    return i >= 0 ? Number(args[i + 1]) : d;
  };
  const failures = await run(args[0], { attempts: opt("--attempts", 30), waitMs: opt("--wait", 10) * 1000 });
  for (const f of failures) console.log(`::error title=Smoke check::${f}`);
  process.exit(failures.length ? 1 : 0);
}
