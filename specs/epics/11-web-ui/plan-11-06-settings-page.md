# Implementation Plan — Story 11.6 Settings Page

> Companion to [story-11-06-settings-page.md](story-11-06-settings-page.md).
> Backed by `/api/settings`, `/api/settings/stt-backends`, `/api/libraries`,
> `/api/me/pats` (Story 11.13), `/api/me/sessions` (Story 11.14).
> Library purge confirmation per REVIEW §5.6 (typed name → `?confirm=`).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Route | `/settings` with sub-routes `/settings/{section}` for permalink. |
| Sections | Libraries · STT Backends · Search · Playback · Account · Appearance · About. |
| Placement | `web/src/routes/settings/`, `web/src/features/settings/`. |
| Form library | React Hook Form + Zod; schemas live next to each section. |
| Optimistic locking | All `PATCH` calls send `If-Unmodified-Since: <updated_at>` (HTTP-date from the row's `updated_at` timestamp); 412 / 409 surfaces "Reload and merge". `If-Match` is reserved for opaque ETags — we ship a timestamp, so `If-Unmodified-Since` is the standards-correct choice. |
| Out of scope | Auth flows (Epic 10); telemetry opt-in (Epic 16); admin "Unlock user" backend (Epic 10 Story 10.11 owns). |

## 1. Component tree

```
<SettingsLayout>
 ├─ <SettingsNav>     left rail; one item per section
 └─ <Outlet>
      ├─ <LibrariesSection>     list + add/edit/delete (purge with name confirm)
      ├─ <STTBackendsSection>   per-backend health, config, "Test"
      ├─ <SearchSection>        hybrid weights, default mode, group, top-K
      ├─ <PlaybackSection>      default subs/audio/rate/quality cap, data-saver
      ├─ <AccountSection>       password, sessions list (Story 11.14), PATs (Story 11.13)
      ├─ <AppearanceSection>    theme, UI language, density
      └─ <AboutSection>         version, build, uptime, license, changelog link
```

## 2. File layout

| Path | Purpose |
|---|---|
| `web/src/routes/settings/index.tsx` | Lazy route + nested routes. |
| `web/src/features/settings/SettingsLayout.tsx` | Shell + nav. |
| `web/src/features/settings/sections/LibrariesSection.tsx` | List + `<LibraryFormDialog>` + `<PurgeConfirmDialog>`. |
| `web/src/features/settings/sections/STTBackendsSection.tsx` | Backend cards + `<BackendTestDialog>`. |
| `web/src/features/settings/sections/SearchSection.tsx` | Sliders for FTS/Semantic weights; mode/topK selectors. |
| `web/src/features/settings/sections/PlaybackSection.tsx` | Default subtitle/audio/rate/quality picker. |
| `web/src/features/settings/sections/AccountSection.tsx` | Embeds `<TokensManager>` and `<SessionsManager>`. |
| `web/src/features/settings/sections/AppearanceSection.tsx` | Theme + language + density. |
| `web/src/features/settings/sections/AboutSection.tsx` | Calls `/api/system/version`. |
| `web/src/features/settings/components/PurgeConfirmDialog.tsx` | Typed-name guard → `?confirm={name}` on DELETE. |
| `web/src/features/settings/components/BackendTestDialog.tsx` | `POST /api/settings/stt-test` flow. |
| `web/src/features/settings/components/TokensManager.tsx` | Story 11.13 UI: list + create + revoke (one-time plaintext display). |
| `web/src/features/settings/components/SessionsManager.tsx` | Story 11.14 UI: list + per-row revoke + bulk revoke-others. |
| `web/src/features/settings/api.ts` | Typed wrappers for `/api/settings*`. |
| `web/src/features/settings/schemas.ts` | Zod schemas per section. |

## 3. Data model

```ts
type LibraryDef = {
  id: string; name: string; path: string;
  sttBackend: 'whisper-mlx'|'whisper-cpu'|'whisper-cuda'|'openai-api';
  monthlyCapUsd?: number;
  updatedAt: string;       // optimistic-lock token
};

type SttBackendInfo = {
  id: string; status: 'ok'|'unavailable';
  modelSize?: string; capUsd?: number; usageUsdMonth?: number;
  capabilities: { gpu: boolean; max_concurrent: number };
};

type SearchPrefs = { ftsWeight: number; semWeight: number; defaultMode: 'fts'|'semantic'|'hybrid'; defaultTopK: number; groupBySegment: boolean; };
type PlaybackPrefs = { subLang: string|null; audioLang: string|null; rate: number; qualityCap: 'auto'|'1080p'|'720p'|'480p'; dataSaverMobile: boolean; };
type AppearancePrefs = { theme: 'light'|'dark'|'system'; uiLang: 'ar'|'en'; density: 'comfortable'|'compact'; };
```

## 4. Implementation steps

### 4.1 Optimistic-lock helper

```ts
async function patchSettings(payload, updatedAt: string) {
  const res = await api.patch('/settings', payload, {
    headers: { 'If-Unmodified-Since': new Date(updatedAt).toUTCString() },
  });
  if (res.status === 412 || res.status === 409) throw new ConflictError(res.data);
  return res.data;
}
```

UI catches `ConflictError`, shows a `<ReloadAndMergeDialog>` that refreshes and re-applies the user's edit on top. Server emits `412 Precondition Failed` when `updated_at` has advanced past the supplied timestamp; `409` is reserved for semantic conflicts (e.g., same-name library exists). Rationale: the client only has a timestamp, not an opaque ETag; `If-Unmodified-Since` is the HTTP-spec-correct precondition for that. If we later add server-emitted ETags on `GET`, we can switch to `If-Match: "<etag>"` without changing the conflict-handling UI.

### 4.2 Libraries section

- Add: `<LibraryFormDialog>` (name + path + STT backend) → `POST /api/libraries`. After 201 the row appears with a "Scan now" affordance. "Scan now" → `POST /api/libraries/{id}/scan` (Idempotency-Key included).
- Edit: same form pre-filled; `PATCH /api/libraries/{id}` with `If-Unmodified-Since: <updated_at>`.
- Delete: opens `<PurgeConfirmDialog>`. Two paths:
  - `purge=false`: confirms unlinking only.
  - `purge=true`: requires the user to type the library name; submit becomes `DELETE /api/libraries/{id}?confirm={typedName}` (REVIEW §5.6).
- 422 `path-not-found` surfaces inline next to the path field.

### 4.3 STT Backends section

- `GET /api/settings/stt-backends` → status + capabilities.
- "Test" button posts `/api/settings/stt-test` with `{ backend, sample? }`. If server responds `no_test_fixture`, swap UI to "Run smoke transcribe on any 30-second video" affordance.
- Switching a library's backend opens `<BackendChangeConfirm>` warning about future cost / in-flight jobs continuing on the old backend.
- "Set monthly cap" inline editor; on a job-claim refusal (`429 budget-exceeded`), the queue dashboard surfaces the explanation; here we display current spend ÷ cap.

### 4.4 Account section

- Password change: `<PasswordForm>` (current + new + confirm) → `POST /api/me/password`; backend rule "cannot remove the only admin's password without setting a new one" surfaces as 422 with field-level error.
- Sessions: `<SessionsManager>` (Story 11.14):
  - Lists `/api/me/sessions`; sorted by `last_used_at DESC`; current session pinned.
  - Per-row "Revoke" → `DELETE /api/me/sessions/{id}`. Revoking current session triggers `useAuth().logout()`.
  - "Revoke all other sessions" → `POST /api/auth/logout-all`.
- PATs: `<TokensManager>` (Story 11.13):
  - List `/api/me/pats` (architecture §9.7.1 canonical).
  - "Create token" form (name, scopes multi-select, expires_at?). Response shows the plaintext **once**, with copy + "I've saved it" gate before close.
  - Per-row "Revoke" → `DELETE /api/me/pats/{id}`.

### 4.5 Appearance section

- Theme/density bind directly to Story 11.8's theme provider.
- UI language change calls `i18n.changeLanguage(value)` and updates `document.documentElement.dir` for RTL — Story 11.12 owns the mechanics.

### 4.6 Admin "Unlock user"

Integrated into a separate `<AdminUserList>` view (linked from About → Admin); calls `POST /api/users/{id}/unlock`. Endpoint owned by Epic 10 Story 10.11 per REVIEW §3.2.

## 5. Edge cases

| Case | Handling |
|---|---|
| Concurrent edit by two admins | 409 → ReloadAndMergeDialog. |
| Path not found on server | 422 `path-not-found` inline error. |
| Single-admin password removal | 422 with explanation; rule shown ahead of submit. |
| STT backend has no test fixture | "Run smoke transcribe" affordance opens an internal video picker. |
| Type wrong library name in purge | Delete button stays disabled. |

## 6. Test cases

### 6.1 Unit

| Test | Asserts |
|---|---|
| `purge dialog gates delete on typed name` | Delete disabled until `typed === library.name`. |
| `optimistic lock retry path` | 409 → `ReloadAndMergeDialog`; merge re-applies field diff. |
| `password change rule surfaces` | Mock 422 → field error visible. |
| `pat plaintext shown once` | Modal carries token; on close, subsequent `GET /api/me/pats` lacks plaintext. |
| `revoking current session triggers logout` | `useAuth.logout` called once. |

### 6.2 e2e

| Test | Asserts |
|---|---|
| `add /mnt/films, scan now, see job` | Library appears; scan job lands within 2 s on the queue page. |
| `switch library backend mlx → openai-api` | Confirmation surfaces; future scans use new backend. |
| `change UI to Arabic flips layout` | `document.dir` = `rtl`; previously visited views re-render correctly. |
| `issue + revoke PAT` | One-time plaintext shown; later revoke returns 401 on use. |

## 7. Dependencies

- Stories 11.13 (PATs), 11.14 (sessions) — embedded as components.
- Epic 7 Stories 7.3 (libraries), 7.15 (settings).
- Epic 9 (library config) for backend-side validation.
- Epic 10 Stories 10.5, 10.11 for sessions + unlock endpoints.
