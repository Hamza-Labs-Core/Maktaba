# Implementation Plan — Story 28.2 Update check service

> Companion to [story-28-02-update-check-service.md](story-28-02-update-check-service.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `api/internal/system/updater.go` (lives beside `version.go`/`aggregator.go`). |
| Source | GitHub Releases API; `owner/repo` from `MAKTABA_UPDATE_REPO` (default `Hamza-Labs-Core/Maktaba`). |
| Auth | Optional `MAKTABA_GITHUB_TOKEN` (raises rate limit). |
| Cache | In-memory `sync.RWMutex` + TTL, keyed by channel. |
| Background | One goroutine started by `main.go` with a ticker = interval; `interval=0` disables. |
| Route | `GET /api/system/updates` mounted in `router/p28.go`. |

## 1. Types

```go
type Asset struct {
    Name string `json:"name"`
    URL  string `json:"url"`   // browser_download_url
    Size int64  `json:"size"`
}

type UpdateStatus struct {
    Available      bool      `json:"available"`
    Disabled       bool      `json:"disabled,omitempty"`
    CurrentVersion string    `json:"current_version"`
    LatestVersion  string    `json:"latest_version,omitempty"`
    Channel        string    `json:"channel"`
    ReleaseURL     string    `json:"release_url,omitempty"`
    ReleaseNotes   string    `json:"release_notes,omitempty"`
    Assets         []Asset   `json:"assets,omitempty"`
    CheckedAt      time.Time `json:"checked_at"`
}

type Updater struct {
    repo     string         // "owner/repo"
    current  string         // running version
    channel  string         // stable|beta
    ttl      time.Duration
    httpc    *http.Client
    token    string
    now      func() time.Time
    disabled bool

    mu        sync.RWMutex
    cached    map[string]UpdateStatus // key: channel
}
```

## 2. GitHub fetch + select

```go
// GET /repos/{repo}/releases?per_page=30
type ghRelease struct {
    TagName    string     `json:"tag_name"`
    HTMLURL    string     `json:"html_url"`
    Body       string     `json:"body"`
    Draft      bool       `json:"draft"`
    Prerelease bool       `json:"prerelease"`
    Assets     []ghAsset  `json:"assets"`
}
```

`selectLatest(releases, channel)`:
- skip `Draft`; for `stable` skip `Prerelease` **and** tags with a
  `-beta`/`-rc`/`-alpha` suffix;
- parse each remaining tag with `parseSemver`; keep the max;
- ignore unparseable tags.

Reuse a small `compareSemver(a,b)` (numeric per-segment, leading `v`
stripped, prerelease ranks below its release — `1.5.0-rc.1 < 1.5.0`).
This is the one place semver correctness matters; pinned by T04/T10.

## 3. Cache + check

```go
func (u *Updater) Status(ctx context.Context, refresh bool) UpdateStatus {
    if u.disabled {
        return UpdateStatus{Disabled: true, CurrentVersion: u.current, Channel: u.channel, CheckedAt: u.now()}
    }
    if !refresh {
        if s, ok := u.fromCache(); ok { return s }
    }
    s, err := u.check(ctx)
    if err != nil {
        if s, ok := u.fromCache(); ok { return s } // serve stale
        return UpdateStatus{CurrentVersion: u.current, Channel: u.channel, CheckedAt: u.now()}
    }
    u.store(s)
    return s
}
```

`fromCache` honours TTL (`now - CheckedAt < ttl`). Errors are logged
rate-limited (don't spam on a long outage).

## 4. Background loop

```go
func (u *Updater) Run(ctx context.Context) {
    if u.disabled || u.ttl <= 0 { return }
    _ = u.Status(ctx, true) // prime at boot
    t := time.NewTicker(u.ttl)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C: _ = u.Status(ctx, true)
        }
    }
}
```

`main.go`: build `Updater` from env (`MAKTABA_UPDATE_CHANNEL`,
`MAKTABA_UPDATE_INTERVAL` default 24h, `MAKTABA_UPDATE_DISABLE`,
`MAKTABA_UPDATE_REPO`, `MAKTABA_GITHUB_TOKEN`), `go u.Run(ctx)`, and pass
it to `MountP28`.

## 5. Handler

```go
func (u *Updater) Handler() http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        refresh := r.URL.Query().Get("refresh") == "true"
        common.WriteJSON(w, r, http.StatusOK, u.Status(r.Context(), refresh))
    })
}
```

Public read (same group as `/api/system/version`); no admin gate — it
exposes only public release metadata.

## 6. Test plan

| Test | Pins |
|---|---|
| `TestSelectLatestStableSkipsPrerelease` | T01/T02 |
| `TestCompareSemverNumeric` (`1.10.0>1.9.9`, rc<release) | T04/T10 |
| `TestStatusCacheWithinTTL` (httptest counts hits) | T05 |
| `TestStatusRefreshBypassesCache` | T06 |
| `TestDisabledNoNetwork` | T07 |
| `TestStatusServesStaleOnError` (stub 403) | T09 |
| `TestHandlerEnvelope` (httptest GitHub) | T08 |

## 7. Acceptance checklist

- [ ] Fetch + channel-aware select + semver compare.
- [ ] TTL cache keyed by channel; refresh bypass.
- [ ] Disable kill-switch (no network).
- [ ] Stale-on-error; rate-limited logging.
- [ ] Background loop wired in `main.go`.
- [ ] `GET /api/system/updates` mounted; tests green.
