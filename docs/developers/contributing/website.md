# maktaba.tv: how the website is built and deployed

maktaba.tv is the site households find Maktaba on (Epic 60): the front door, the install guides,
and later the download, help, problem and legal pages, each in English and Arabic. It lives in
`web/packages/website` ([README](https://github.com/Hamza-Labs-Core/Maktaba/tree/main/web/packages/website)),
and is built and deployed the way this portal is: a Cloudflare Worker in front of static files.

## The build

`npm run build` in `web/packages/website` writes `dist/`, in this order:

1. the install build (`pnpm -C web build:install`, Epic 59's guides), copied to the root of the
   site at `/install/` and `/ar/install/`, its hashed files under `/install/assets/`;
2. the generated families (the problem pages, help, download), as their stories add them;
3. the hand-made pages (`src/pages/`), each wrapped in the page shell (`scripts/shell.mjs`);
4. the copied files: the log schema at `/schema/log-line.json` (its `$id`), `security.txt` with a
   `Canonical` line for each domain, and `robots.txt`;
5. the checks: every page has its twin in the other language, and an Arabic page passes this
   portal's Latin-share check.

## The Worker

`src/worker.ts` sends `www.maktaba.tv` and plain http to `https://maktaba.tv`, sends `/docs` to
the front door (to `/help/` once the help pages exist), answers a missing path with the 404 page
in its language and status 404, and puts the headers on every answer: a strict CSP, HSTS without
`includeSubDomains` (the relay's names must not inherit it), and the cache policy.

## Pull requests

`ci.yml`'s `website` job (`_website.yml`) runs on a pull request that changes the site or a
source it is built from: the build, the unit tests (the Worker, the build, the smoke and the
deploy steps), and a smoke of the built site through the Worker under `wrangler dev`, whose log
is kept as the run's `website-worker-log` artifact. The built site is the `website-site`
artifact.

## The deploy

`website-deploy.yml` runs on a push to `main` that changes the site, by hand, and monthly. Its
steps (`tools/cloudflare/website-deploy.mjs`):

1. the token check (`token-check.mjs --pieces workers,dns`);
2. record the version serving now (`wrangler deployments status`): a rollback returns to it;
3. upload the new version, not yet live (`wrangler versions upload`), and smoke its preview URL;
4. promote it at 100% (`wrangler versions deploy`), then apply the custom domains
   (`wrangler triggers deploy`): `maktaba.tv` and `www.maktaba.tv`, and no other name;
5. smoke the live site; when that fails, roll back to the recorded version and fail.

A first deploy creates the Worker with no routes before step 3, because a version cannot be
uploaded to a Worker that does not exist. If its live smoke fails, the custom domains are
detached again.

## Going live

The deploy goes live only when all three are set: the secret `CLOUDFLARE_API_TOKEN_WEBSITE` (the
website's own token, with DNS Edit on `maktaba.tv`, because custom domains write their records),
the secret `CLOUDFLARE_ACCOUNT_ID`, and the repository variable `WEBSITE_LIVE` set to `true`.
Without any of them the job prints a notice naming what is missing and passes. The shared
`CLOUDFLARE_API_TOKEN`, which deploys this portal, is never read, so maktaba.tv cannot go live by
accident. `deploy/cloudflare/README.md` has the token's permissions.

## Rolling back

Actions → **Deploy maktaba.tv** → Run workflow, with `rollback: true`. Leave `version` empty to
return to the deployment before the current one, or give a version id from a deploy's run
summary. The live smoke runs after the rollback, and the run fails if it does not pass.
