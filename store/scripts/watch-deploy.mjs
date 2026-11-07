// store-deploy.yml's gradual deployment check (story 54.15 §11): while the new Worker version
// takes 10 % of traffic, read its errors and requests from Cloudflare's GraphQL Analytics
// every minute for `seconds`; fail (and the workflow rolls back) when its error rate rises
// past 1 % with at least 20 errors. Reads CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID.
//
//   node scripts/watch-deploy.mjs <version id> <seconds>
const [version, seconds] = process.argv.slice(2);
const token = process.env.CLOUDFLARE_API_TOKEN;
const account = process.env.CLOUDFLARE_ACCOUNT_ID;
if (!version || !seconds || !token || !account) {
  console.error("usage: watch-deploy.mjs <version> <seconds> (with CLOUDFLARE_API_TOKEN, CLOUDFLARE_ACCOUNT_ID)");
  process.exit(2);
}
const start = new Date();
const query = `query ($account: String!, $since: Time!, $version: String!) {
  viewer { accounts(filter: {accountTag: $account}) {
    workersInvocationsAdaptive(limit: 100, filter: {scriptName: "store-web", datetime_geq: $since, scriptVersion: $version}) {
      sum { requests errors }
    }
  } }
}`;
async function sample() {
  const r = await fetch("https://api.cloudflare.com/client/v4/graphql", {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({ query, variables: { account, since: start.toISOString(), version } }),
  });
  const j = await r.json();
  const rows = j?.data?.viewer?.accounts?.[0]?.workersInvocationsAdaptive ?? [];
  return rows.reduce((a, x) => ({ requests: a.requests + (x.sum?.requests ?? 0), errors: a.errors + (x.sum?.errors ?? 0) }), {
    requests: 0,
    errors: 0,
  });
}
const until = Date.now() + Number(seconds) * 1000;
while (Date.now() < until) {
  await new Promise((r) => setTimeout(r, 60_000));
  const s = await sample();
  const rate = s.requests ? s.errors / s.requests : 0;
  console.log(`version ${version}: ${s.requests} requests, ${s.errors} errors (${(rate * 100).toFixed(2)} %)`);
  if (s.errors >= 20 && rate > 0.01) {
    console.error("the error rate rose: rolling back");
    process.exit(1);
  }
}
