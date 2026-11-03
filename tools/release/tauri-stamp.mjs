// Stamps apps/desktop/src-tauri/tauri.conf.json for a release build: no frontend rebuild
// (web/dist is built before), the version from the tag, WiX's numeric version for a
// pre-release, and with the updater key the updater artifacts and the public key the apps
// check their updates against. desktop-release.yml's legs and CI's bundle-size job (the Core
// leg's dry run, with a throwaway key) both stamp through here.
//
// env: TAG (vX.Y.Z[-pre]), UPDATER_KEY ("true" when the signing key is set),
//      UPDATER_PUBKEY (the key's public half; required with UPDATER_KEY)
import fs from 'node:fs';

const f = 'apps/desktop/src-tauri/tauri.conf.json';
const c = JSON.parse(fs.readFileSync(f, 'utf8'));
c.build.beforeBuildCommand = '';
const v = (process.env.TAG || '').replace(/^v/, '');
if (v) c.version = v;
const core = v.split(/[-+]/)[0];
if (core !== v) c.bundle.windows.wix.version = core;
if (process.env.UPDATER_KEY === 'true') {
  const pub = (process.env.UPDATER_PUBKEY || '').trim();
  if (!pub) {
    console.log('::error::TAURI_SIGNING_PRIVATE_KEY is set but the repository variable TAURI_UPDATER_PUBKEY is not: the apps could not verify their updates');
    process.exit(1);
  }
  c.bundle.createUpdaterArtifacts = true;
  c.plugins.updater.pubkey = pub;
} else {
  console.log('::notice::no TAURI_SIGNING_PRIVATE_KEY: bundles without updater artifacts (no latest.json)');
}
fs.writeFileSync(f, JSON.stringify(c, null, 2) + '\n');
console.log('version=' + c.version + ' wix.version=' + (c.bundle.windows.wix.version || '(from version)') +
  ' updater=' + (c.bundle.createUpdaterArtifacts === true));
