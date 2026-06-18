# Implementation Plan — Story 28.1 Versioning & build stamping

> Companion to [story-28-01-versioning-build-stamping.md](story-28-01-versioning-build-stamping.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Go stamping | Already in `Makefile` (`api_ldflags`/`streaming_ldflags`/`server_ldflags`) and `release.yml`. Verify uniform; no rework. |
| `channel` field | Derived in `api/internal/version` (`Channel()`), surfaced by `system.VersionHandler`. |
| Web `version.json` | Written by `web` build (`scripts/write-version.mjs`, run in `package.json build`); `VITE_APP_VERSION` defined via Vite `define`. |
| Tauri version | Set from `github.ref_name` in `desktop-release.yml` (in the existing Node config-rewrite step). |
| Android version | `versionName`/`versionCode` patched from the tag in `mobile-release.yml` after `cap add`. |

## 1. `channel` derivation

```go
// api/internal/version/version.go
func Channel() string {
    if c := strings.TrimSpace(os.Getenv("MAKTABA_UPDATE_CHANNEL")); c != "" {
        return c // operator override wins
    }
    v := strings.ToLower(Version)
    if strings.Contains(v, "-beta") || strings.Contains(v, "-rc") ||
        strings.Contains(v, "-alpha") || strings.Contains(v, "nightly") {
        return "beta"
    }
    return "stable"
}
```

`system.VersionInfo` gains `Channel string` and `VersionHandler` fills it
from `version.Channel()`. Existing fields (`version`, `build_sha`,
`build_time`, `go_version`, `schema_revision`) are unchanged so existing
consumers don't break; `channel` is additive.

## 2. Web `version.json` + `VITE_APP_VERSION`

`web/scripts/write-version.mjs` (runs as part of `pnpm build`):

```js
import { writeFileSync, mkdirSync } from "node:fs";
const version = process.env.VERSION || "dev";
const commit = process.env.COMMIT || "unknown";
const buildDate = process.env.SOURCE_DATE_EPOCH || "unknown";
mkdirSync("dist", { recursive: true });
writeFileSync("dist/version.json",
  JSON.stringify({ version, commit, build_date: buildDate }) + "\n");
```

`package.json`: `"build": "vite build && node scripts/write-version.mjs"`.
`vite.config.ts`: `define: { "import.meta.env.VITE_APP_VERSION": JSON.stringify(process.env.VERSION || "dev") }`.

Because every CI build path already exports `VERSION` (and `make
build-web` inherits it), `version.json` is correct everywhere the bundle
is built.

## 3. Tauri version from tag

In `desktop-release.yml`'s existing "Neutralise in-config frontend
rebuild" Node step, also set `c.version` from the tag:

```js
const tag = process.env.TAG.replace(/^v/, "");
c.version = tag;            // drives updater current-version + installer product version
c.build.beforeBuildCommand = "";
```

with `env: { TAG: ${{ github.ref_name }} }` added to the step.

## 4. Android version from tag

In `mobile-release.yml`, after `cap add android`, patch the generated
`android/app/build.gradle`:

```bash
ver="${TAG#v}"
# versionCode: MAJOR*1_000_000 + MINOR*1_000 + PATCH (monotonic, int32-safe)
IFS='.' read -r MA MI PA <<< "${ver%%-*}"
code=$(( MA*1000000 + MI*1000 + PA ))
sed -i "s/versionName \".*\"/versionName \"${ver}\"/" app/build.gradle
sed -i "s/versionCode [0-9]*/versionCode ${code}/" app/build.gradle
```

## 5. Test plan

| Test | Pins |
|---|---|
| `TestChannelFromVersion` (stable/beta/rc/override) | §1 |
| `TestVersionHandlerIncludesChannel` | §1 |
| web unit: `version.json` shape | §2 |
| CI assertion: `dist/version.json` present after build | §2 |
| `go build` no-ldflags smoke | defaults preserved |

## 6. Acceptance checklist

- [ ] `version.Channel()` + env override; unit-tested.
- [ ] `/api/system/version` returns `channel`.
- [ ] `dist/version.json` written by the web build; `VITE_APP_VERSION` wired.
- [ ] `tauri.conf.json` version set from tag in CI.
- [ ] Android `versionName`/`versionCode` from tag.
- [ ] All Go build paths confirmed stamping (Makefile/release/ci/relay).
