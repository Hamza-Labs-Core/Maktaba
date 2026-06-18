# Implementation Plan — Story 28.6 Update UI

> Companion to [story-28-06-update-ui.md](story-28-06-update-ui.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Settings section | Replace the stub `AboutTab` in `web/src/pages/Settings.tsx`. |
| Data | `GET /api/system/version`, `GET /api/system/updates[?refresh=true]`, `POST /api/admin/system/update`. |
| Admin gate | `useAuth().user.is_admin` controls the "Update now" button. |
| Badge | Settings nav icon + About link when `available`. |
| i18n | `update.*` keys in EN + AR. |

## 1. About/Updates section

```tsx
function AboutTab() {
  const { t } = useI18n();
  const { user } = useAuth();
  const [ver, setVer] = useState<VersionInfo|null>(null);
  const [upd, setUpd] = useState<UpdateStatus|null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.get<VersionInfo>("/api/system/version").then(setVer).catch(()=>{});
    api.get<UpdateStatus>("/api/system/updates").then(setUpd).catch(()=>{});
  }, []);

  const checkNow = async () => {
    setBusy(true);
    try { setUpd(await api.get<UpdateStatus>("/api/system/updates?refresh=true")); }
    finally { setBusy(false); }
  };
  // ...render version block, channel selector, "Check now",
  //    available-card (notes via safe markdown + "View release"),
  //    and the admin "Update now" / docker-instruction branch.
}
```

## 2. Admin one-click update

```tsx
async function runUpdate() {
  setPhase("downloading");
  try {
    await api.post("/api/admin/system/update", { confirm: true });
    setPhase("restarting");
    await pollVersionUntilChanged(ver!.version); // re-GET /api/system/version
    setPhase("done");
  } catch (e) {
    if (e instanceof ApiError && e.status === 409 && e.problem.type.includes("docker")) {
      setDockerInstructions(e.problem.detail); // show instruction, not a failure
    } else setPhase("error");
  }
}
```

Visible only when `user.is_admin && upd.available && !docker`. For docker
the 409 body's instruction is shown instead of the button.

## 3. Badge

A small `useUpdateAvailable()` hook (shared with the nav) reads
`/api/system/updates` once and exposes `available`; the Settings nav
renders a `<span class="mkt-badge" aria-label={t("update.badge")}/>` when
true.

## 4. i18n keys (EN shown; AR mirrored)

```
"update.section.title": "Updates",
"update.current": "Current version",
"update.commit": "Commit",
"update.buildDate": "Build date",
"update.channel": "Update channel",
"update.channel.stable": "Stable",
"update.channel.beta": "Beta",
"update.lastChecked": "Last checked",
"update.checkNow": "Check now",
"update.checking": "Checking…",
"update.upToDate": "You're up to date.",
"update.available": "Update available: {version}",
"update.viewRelease": "View release",
"update.updateNow": "Update now",
"update.phase.downloading": "Downloading…",
"update.phase.verifying": "Verifying…",
"update.phase.restarting": "Restarting…",
"update.phase.done": "Updated. Now running {version}.",
"update.phase.error": "Update failed.",
"update.rolledBack": "Update failed and was rolled back.",
"update.docker.instructions": "This is a Docker install. Run:",
"update.disabled": "Automatic update checks are off.",
"update.badge": "Update available",
"update.banner.available": "New version available — {version}",
"update.banner.get": "Get update",
"update.banner.notes": "Release notes",
"common.dismiss": "Dismiss"
```

## 5. Test plan

| Test | Pins |
|---|---|
| renders version fields from stub | T01 |
| available → notes card + link | T02 |
| admin+binary → "Update now"; non-admin hidden | T03/T04 |
| docker 409 → instruction, no button | T05 |
| badge present when available | T06 |
| axe clean; badge labelled | T07 |
| ar locale translated + `dir=rtl` | T08 |
| "Check now" hits `?refresh=true` | T09 |

## 6. Acceptance checklist

- [ ] About section: version/commit/build-date/channel/last-checked.
- [ ] Channel selector persists; recheck on change.
- [ ] Available card with rendered notes + release link.
- [ ] Admin one-click update with progress + version re-poll.
- [ ] Docker/deb instruction path.
- [ ] Nav badge when available.
- [ ] EN + AR; a11y clean; tests green.
