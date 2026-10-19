# Admin machines (cluster)

- **Route:** `/admin/machines` (no params; the active tab is component state, not in the URL)
- **Component:** `src/pages/Admin/Machines.tsx` (`Machines` -> `AdminGate` -> `MachinesPage` with DS `Tabs`: Machines, Jobs, Queues, Library, Logs, LLM, Relay). Machines tab = `MachinesOverview` -> `Approvals` (`HomeCard` with `StorageCheck`, `CanonicalizePanel`, `PathMapEditor`, `StorageServers`; `PendingRow`; `MachineCard` with `StageTable`, `PathMapEditor`, revoke/remove `Modal`), `RelayDoorCard`, `LiveMachines`. Other tabs: `src/components/cluster/JobsPanel.tsx`, `QueuesPanel.tsx`, `LibraryPanel.tsx`, `LogsPanel.tsx` (+ `LogViewer.tsx`), `LlmPanel.tsx`, `RelayPanel.tsx`. Helpers: `machineSummary.ts` (`reconcileView`, `sortNodes`, `jobCounts`), `useClusterFiles.ts`, `format.ts`, `names.ts`. (`Processes.tsx` is rendered on `/admin/health`, and `CloudflareRemote.tsx` / `RemoteUsage.tsx` on Extensions / Settings, not here.)
- **API calls** (`src/lib/clusterApi.ts`, all through `api` in `src/lib/api.ts`):
  - `GET /api/cluster` (`clusterApi.getCluster`) -> `ClusterView { home: ClusterHome, nodes: ClusterNode[] }`; polled every 5 s (`POLL_MS`).
  - `PUT /api/cluster/settings` `{ accept_joins }`; `POST /api/cluster/nodes/{id}/approve` `{ code }`; `POST .../{id}/reject`; `POST .../{id}/revoke`; `PUT /api/cluster/nodes/{id}` `NodeUpdate` (`drain`, `path_map`, `stages`); `DELETE /api/cluster/nodes/{id}`; `PUT /api/cluster/home/path-map` `{ path_map }`.
  - `GET /api/cluster/storage-servers` -> `{ servers: StorageServer[] }` (once on mount); `PUT /api/cluster/storage-servers/{name}` `{ addresses, char_map }`; `DELETE /api/cluster/storage-servers/{name}`; `POST /api/cluster/storage/canonicalize` `{ action }`.
  - `GET /api/cluster/relay` (`clusterChannelApi.relay`) -> `RelayStatus { available, enabled, settings, extensions[], home_only[], calls[], totals[] }`; polled every 5 s by `RelayDoorCard` and by `RelayPanel`. Relay on/off: `PATCH /api/rules/relay` via `rulesApi`.
  - `GET /api/cluster/files` (`useClusterFiles`) -> `{ files, nodes: FileNode[] }`; per-tab `GET /api/cluster/files/{ip}/logs|queues|library?...`.
  - WebSocket `/api/cluster/ws` (`src/lib/clusterSocket.ts`, `useClusterStream` in `src/lib/clusterStream.ts`): processes snapshot, metrics, logs, llm, jobs, queues, library frames. Status badge from socket status: Connecting… / Live / Reconnecting… / Live view unavailable.
- **Access:** signed-in admin only (`RequireAuth` -> `/login`; `AdminGate` -> "Admins only"); server 403s.
- **Existing tests:**
  - `src/pages/Admin/Machines.test.tsx` — covers: home + pending + machine render (capability chips, path map, stage served/off reason); roles unsupported disables toggle; turn accept on; time left while open; wrong code with tries left; expired code; approve sends digits only; non-6-digit code refused client-side; reject; stage off / back to auto (null); drain; edit + reset path map; shares list; home mounts + storage servers edit; revoke confirm; remove only revoked/rejected; non-admin gate (no GET); paused-by-schema notice en + ar; web link / version / kind; non-http link not linked; VOD fallback note; Arabic RTL; axe; 5 s re-read.
  - `src/pages/Admin/MachinesStable.test.tsx` — sort order, object reuse, job counts, skeleton on first load only, stable DOM across polls, only changed card re-renders, jobs count links to `/queue?host=...`, hub frame batching.
  - `src/components/cluster/ClusterViews.test.tsx` — live tabs from the socket: machines/processes, metrics sparklines, disconnected process, commands, disconnect confirm, live log tail, archived log days, LLM models + feed + try-model, job events, queue charts, library numbers, Arabic, axe, "cannot connect" message.
  - `RelayPanel.test.tsx`, `RelayDoorCard.test.tsx`, `CanonicalizePanel.test.tsx` — relay panel happy path, toggle, no door, Arabic, axe; door card stable layout and error; canonicalize states and revert/resume.
  - Gaps: `GET /api/cluster` errors (banner, retry, stale line), every null-list shape, `home: null`, toggle/approve/revoke API failures, storage-server delete/add validation, crash recovery, tab navigation by keyboard in RTL, phone width, theme.

Notes for the implementer: reuse the `fixture()` / `baseNode` from `Machines.test.tsx`, its `api` mock (`get` switch by URL) and the `FakeSocket` from `ClusterViews.test.tsx` (`vi.stubGlobal("WebSocket", FakeSocket)`).

## Summary

| ID     | Title                                                                    | Category      | Priority | Covered by existing test?                                                   |
| ------ | ------------------------------------------------------------------------ | ------------- | -------- | --------------------------------------------------------------------------- |
| MCH-01 | Home, pending request and joined machine render                          | Happy path    | P1       | Yes (`Machines.test.tsx`)                                                   |
| MCH-02 | Non-admin gate; signed-out redirect                                      | Permissions   | P1       | Yes (Machines.cases.test.tsx)                                               |
| MCH-03 | Skeleton on first load only                                              | Loading       | P2       | Yes (`MachinesStable.test.tsx`)                                             |
| MCH-04 | First load fails: alert banner and Try again                             | Error         | P1       | Yes (Machines.cases.test.tsx)                                               |
| MCH-05 | Later poll fails: stale line, data kept, recovers                        | Error         | P2       | Yes (Machines.cases.test.tsx)                                               |
| MCH-06 | `nodes: []` shows both empty texts                                       | Empty state   | P2       | Yes (Machines.cases.test.tsx)                                               |
| MCH-07 | `nodes: null` / missing shows empty lists, not an error                  | Empty state   | P1       | Yes (Machines.cases.test.tsx)                                               |
| MCH-08 | `home: null` / missing does not blank the page                           | Error         | P1       | Yes (Machines.cases.test.tsx)                                               |
| MCH-09 | Node nested lists null: workers, stages, stages_off, shares, overrides   | Empty state   | P1       | Yes (Machines.cases.test.tsx)                                               |
| MCH-10 | Storage servers: `servers: null`, `addresses: null`                      | Empty state   | P1       | Yes (Machines.cases.test.tsx)                                               |
| MCH-11 | Relay: `extensions`, `home_only`, `calls`, `totals`, `allow` null        | Empty state   | P1       | Yes (Machines.cases.test.tsx)                                               |
| MCH-12 | Canonicalize: `problems`, `servers_added[].addresses` null               | Empty state   | P2       | Yes (Machines.cases.test.tsx)                                               |
| MCH-13 | Files: `nodes: null`, processes/dates null                               | Empty state   | P2       | Yes (Machines.cases.test.tsx)                                               |
| MCH-14 | Accept-joins toggle failure shows a toast and re-enables                 | Error         | P2       | Yes (Machines.cases.test.tsx)                                               |
| MCH-15 | Approve: wrong code, no count, expired, other error, 0 tries left        | Validation    | P1       | Yes (Machines.cases.test.tsx)                                               |
| MCH-16 | Pending request already expired disables the form                        | Validation    | P2       | Yes (Machines.cases.test.tsx)                                               |
| MCH-17 | Revoke / remove: Cancel, Escape, API failure                             | Error         | P1       | Yes (Machines.cases.test.tsx)                                               |
| MCH-18 | Path map editor: blank rows dropped, remove row, cancel                  | Validation    | P2       | Yes (Machines.cases.test.tsx)                                               |
| MCH-19 | Storage server: blank name ignored, address parsing, delete, failure     | Validation    | P2       | Yes (Machines.cases.test.tsx)                                               |
| MCH-20 | Node status badges: online, offline, draining, paused, revoked, rejected | Happy path    | P2       | Yes (Machines.cases.test.tsx)                                               |
| MCH-21 | Live status badge and socket unavailable                                 | Error         | P2       | Partial (`ClusterViews.test.tsx`: "says when the live view cannot connect") |
| MCH-22 | Tabs switch panels; "open logs" jumps to the Logs tab                    | Navigation    | P2       | Partial (`ClusterViews.test.tsx`: tabs used, not keyboard)                  |
| MCH-23 | Jobs link goes to the filtered queue page; Back returns                  | Navigation    | P2       | No (e2e-only)                                                               |
| MCH-24 | Render crash shows route error fallback; Back still works                | Error         | P1       | Yes (Machines.cases.test.tsx)                                               |
| MCH-25 | Polling and socket stop after leaving the page                           | Happy path    | P2       | Yes (Machines.cases.test.tsx)                                               |
| MCH-26 | Arabic: RTL, Arabic machine names, LTR codes and paths                   | RTL           | P2       | Yes (Machines.cases.test.tsx)                                               |
| MCH-27 | Phone width: cards, stage table and tabs fit                             | Responsive    | P3       | No                                                                          |
| MCH-28 | Light and dark theme                                                     | Theme         | P3       | No                                                                          |
| MCH-29 | Keyboard: tabs arrows (incl. RTL), dialogs trap focus, live regions      | Accessibility | P3       | Partial (`Machines.test.tsx`, `ClusterViews.test.tsx`: axe only)            |

## Cases

### MCH-01 — Home, pending request and joined machine render

- **Priority:** P1
- **Category:** Happy path
- **Covered by existing test?** Yes — `Machines.test.tsx` "renders the home, the pending request and the machine"
- **Preconditions / data:** admin; `GET /api/cluster` -> `fixture()` from `Machines.test.tsx` (home "Home NAS box", pending "Linux box", approved "Studio Mac").
- **Steps:**
  1. Open `/admin/machines`.
- **Expected:** h1 "Machines"; "This home" card with "Home NAS box" and switch "Accept new machines"; "Asking to join" lists "Linux box" with "Linux · x86_64" and input "Code shown on Linux box"; "Joined machines" has article "Studio Mac" with "Online", "10 CPUs", "32 GB memory", "Apple Silicon", the path map and the stage table.

### MCH-02 — Non-admin gate; signed-out redirect

- **Priority:** P1
- **Category:** Permissions
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** (a) `useAuth` -> `{ user: { is_admin: false } }`; (b) full app, auth bootstrap 401.
- **Steps:**
  1. Open `/admin/machines`.
- **Expected:** (a) "Admins only"; no `GET /api/cluster`, `/api/cluster/relay`, `/api/cluster/files`, `/api/cluster/storage-servers`; no `WebSocket` constructed for `/api/cluster/ws` (check: `useClusterStream` and `useClusterFiles` live in `MachinesPage`, which is inside `AdminGate`, so nothing should start). (b) redirect to `/login` with `state.from = "/admin/machines"`.

### MCH-03 — Skeleton on first load only

- **Priority:** P2
- **Category:** Loading
- **Covered by existing test?** Yes — `MachinesStable.test.tsx` "shows the skeleton on the first load only..."
- **Preconditions / data:** admin; first `GET /api/cluster` pending.
- **Steps:**
  1. Open `/admin/machines`.
- **Expected:** `.mkt-mc-skeleton[aria-busy="true"]` with a visually hidden "Loading…" `role="status"`; after data, never shown again on later polls.

### MCH-04 — First load fails: alert banner and Try again

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; `GET /api/cluster` rejects with each of: `ApiError` 500, `ApiError` 403, `ApiError` 401, `TypeError("Failed to fetch")`, 200 with truncated JSON. Then resolves `fixture()`.
- **Steps:**
  1. Open `/admin/machines`.
  2. Click "Try again".
- **Expected:** Step 1: `role="alert"` banner "Couldn't load the machines." with a "Try again" button; the relay door card and live section still render below. Step 2: a new `GET /api/cluster`; home and machines appear; banner gone.

### MCH-05 — Later poll fails: stale line, data kept, recovers

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; fake timers; poll 1 `fixture()`, poll 2 rejects 500, poll 3 `fixture()`.
- **Steps:**
  1. Open the page; advance 5 s; advance 5 s again.
- **Expected:** After poll 2: status line "Couldn't refresh — showing the last known state." (`role="status"`); cards unchanged (same DOM nodes). After poll 3: the stale text is gone.

### MCH-06 — `nodes: []` shows both empty texts

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; `GET /api/cluster` -> `{ "home": <fixture home>, "nodes": [] }`.
- **Steps:**
  1. Open `/admin/machines`.
- **Expected:** "No machine is asking to join." and "No machine has joined yet."; home card renders.

### MCH-07 — `nodes: null` / missing shows empty lists, not an error

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; `GET /api/cluster` -> (a) `{ "home": <home>, "nodes": null }`, (b) `{ "home": <home> }`. Also (c) poll 1 normal, poll 2 `nodes: null`.
- **Steps:**
  1. Open `/admin/machines` (advance 5 s for c).
- **Expected:** Home card plus "No machine is asking to join." and "No machine has joined yet.". Today `reconcileView` -> `sortNodes([...null])` (machineSummary.ts:44/46) throws inside `.then`, so (a)/(b) show the "Couldn't load the machines." banner and (c) shows the stale line forever, hiding that there are no nodes (currently unhandled — likely bug). Never a blank page.

### MCH-08 — `home: null` / missing does not blank the page

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; `GET /api/cluster` -> (a) `{ "home": null, "nodes": [] }`, (b) `{ "nodes": [] }`, (c) home with `"name": null`, `"accept_joins_until": "not-a-date"`, `"roles_reason": ""`.
- **Steps:**
  1. Open `/admin/machines`.
- **Expected:** (a)/(b): the page renders the rest (pending, machines, relay, live) and the home card shows an error/unknown state. Today `HomeCard` reads `home.accept_joins` (Machines.tsx:372) during render and throws — whole tab blank (currently unhandled — likely bug). (c): renders; name area empty; no time-left text; `roles_supported: false` with empty `roles_reason` falls back to "This home can't take other machines.".

### MCH-09 — Node nested lists null: workers, stages, stages_off, shares, overrides

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; approved node variants (one per run, rest of `baseNode` intact):
  - `workers: null` and `workers` missing
  - `workers: [{ ...w, "stages": null }]`
  - `workers: [{ ...w, "stages_off": null }]`
  - `stage_overrides: null`
  - `storage: { "read": "ok", "write": "ok", "shares": null, "checked_at": null, "detail": null }`
  - `storage: null` and `storage: {}`
  - `path_map: null`, `path_map_source: null`
  - `capabilities: null`, `capabilities: { "gpus": null }`
  - `name: null` (sort uses `a.name.localeCompare`)
- **Steps:**
  1. Open `/admin/machines` for each.
- **Expected:** The card renders each time: no workers -> "No worker has reported in from this machine yet."; null storage / `{}` -> "Not checked yet."; null shares -> no shares list; null path map -> "No share is mounted here yet."; null capabilities -> just OS/arch chips. Today `workers[].stages: null` throws at `w.stages.forEach` (Machines.tsx:1126) and `.includes` (Machines.tsx:1160) during render (currently unhandled — likely bug). `name: null` makes `sortNodes` throw inside `.then`, showing the load-error banner (likely bug). Others are guarded.

### MCH-10 — Storage servers: `servers: null`, `addresses: null`

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; home with `"storage": { "read": "ok", "write": "ok", "checked_at": null, "detail": null }` (so the servers section shows). `GET /api/cluster/storage-servers` -> (a) `{ "servers": null }`, (b) `{}`, (c) `null`, (d) rejects 500, (e) `{ "servers": [ { "id": "s1", "name": "nas", "addresses": null, "char_map": "none", "exports": {} } ] }`.
- **Steps:**
  1. Open `/admin/machines`.
  2. For (e), click "Edit nas".
- **Expected:** (a)-(d): "No storage server yet. Paths stay as the home stores them." (code checks `Array.isArray`). Note (d) hides a load failure as "none" (minor). (e): the row shows `smb://nas/` and "-"; Edit opens with empty addresses. Today `s.addresses.join` (Machines.tsx:511) throws during render (currently unhandled — likely bug).

### MCH-11 — Relay: `extensions`, `home_only`, `calls`, `totals`, `allow` null

- **Priority:** P1
- **Category:** Empty state
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; `GET /api/cluster/relay` -> base `{ "available": true, "enabled": true, "settings": { "requests_per_minute": 60, "burst": 10, "max_in_flight": 4, "max_response_mb": 50, "timeout_sec": 30 }, "extensions": [], "home_only": [], "calls": [], "totals": [] }`, then one field at a time set to `null` or removed: `extensions`, `home_only`, `calls`, `totals`, `extensions[0].allow` (with one extension), `settings`.
- **Steps:**
  1. Open `/admin/machines` (Machines tab shows `RelayDoorCard`).
  2. Click the "Relay" tab.
- **Expected:** Door card: counters and five empty rows; badge "On". Relay tab: "Extension relay" with "On", "No relayed calls since the home started.", no extension sections. Today: door card `summarise` throws on null `totals`/`calls` (RelayDoorCard.tsx:28-34), caught -> badge "Status unknown" (wrong, but no crash). Relay tab throws during render on null `extensions` (RelayPanel.tsx:117), `home_only` (148), `calls` (167), `allow` (134) and on null `settings` (`s.requests_per_minute`) (currently unhandled — likely bug).

### MCH-12 — Canonicalize: `problems`, `servers_added[].addresses` null

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; home with `storage` set and `canonicalize: { "state": { "phase": "done", "problems": null, "servers_added": [ { "name": "nas", "addresses": null } ], "plex_added": null }, "request": null, "requested_at": null, "requested_by": null }`; also `canonicalize: { "state": null }`.
- **Steps:**
  1. Open `/admin/machines`.
- **Expected:** Panel renders; no problems list; server line without addresses. Today `s.addresses.join` (CanonicalizePanel.tsx:109) throws on null addresses (currently unhandled — likely bug). Check `state: null` does not throw.

### MCH-13 — Files: `nodes: null`, processes/dates null

- **Priority:** P2
- **Category:** Empty state
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; `GET /api/cluster/files` -> (a) `{ "files": null, "nodes": null }`, (b) `{ "nodes": [ { "ip": "10.0.0.5", "processes": null, "dates": null, "archived_dates": null } ] }`, (c) rejects 500.
- **Steps:**
  1. Open the "Logs", "Queues" and "Library" tabs.
- **Expected:** (a) Logs tab: `cluster.files.none` text; Queues/Library: no saved days, live data only. (b) node listed by IP with no days text. (c) Logs tab: `role="alert"` "cluster.files.error" text; others still render live data. `useClusterFiles.parse` normalises these, so all should pass — regression guard.

### MCH-14 — Accept-joins toggle failure shows a toast and re-enables

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; `PUT /api/cluster/settings` rejects `ApiError` 409 `{ title: "Conflict", detail: "joins locked" }`.
- **Steps:**
  1. Click switch "Accept new machines".
- **Expected:** Switch disabled while pending; error toast "joins locked"; switch enabled again and back to off after the next poll.

### MCH-15 — Approve: wrong code, no count, expired, other error, 0 tries left

- **Priority:** P1
- **Category:** Validation
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; pending "Linux box". `POST /api/cluster/nodes/p1/approve` rejects with:
  - `{ status: 400, code: "wrong_code" }` (no attempts_left) -> alert "Wrong code."
  - `{ status: 400, type: "https://x/problems/wrong_code", attempts_left: 0 }` -> "Wrong code, 0 tries left." and a reload
  - `{ status: 400, code: "expired" }` (not 410) -> "This request has expired. Ask again from the machine."
  - `{ status: 500, title: "Internal Server Error", detail: "db" }` -> alert "db"
  - `TypeError("Failed to fetch")` -> alert "Failed to fetch"
  - Inputs: "123-456" (accepted), "12345a" and "1234567" (refused, "Enter the 6-digit code."), "١٢٣٤٥٦" (Arabic-Indic digits).
- **Steps:**
  1. Type the code; click "Approve".
- **Expected:** As listed; Approve disabled while busy and while the input is empty/whitespace. Arabic-Indic digits are refused today (`\d` only matches ASCII) — expected: accepted, since Arabic users may type them (currently unhandled — likely bug).

### MCH-16 — Pending request already expired disables the form

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; pending node with `expires_at` 1 minute in the past; another with `expires_at: null`.
- **Steps:**
  1. Open `/admin/machines`.
- **Expected:** Expired: badge "Expired"; code input and Approve disabled; Reject enabled. `expires_at: null`: no time-left text, form enabled.

### MCH-17 — Revoke / remove: Cancel, Escape, API failure

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; approved "Studio Mac"; `POST .../n1/revoke` rejects `ApiError` 500 `{ title: "Internal Server Error", detail: "busy" }`.
- **Steps:**
  1. Click "Revoke"; click "Cancel".
  2. Click "Revoke"; press Escape.
  3. Click "Revoke"; click the backdrop.
  4. Click "Revoke"; confirm "Revoke".
- **Expected:** Dialog title "Revoke Studio Mac?" and body text from `machines.revoke.body`. Steps 1-2 close with no request; step 3 does not close (`dismissable={false}`). Step 4: dialog closes, buttons disabled while busy, error toast "busy", card still "Online".

### MCH-18 — Path map editor: blank rows dropped, remove row, cancel

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; machine with `path_map: { "/mnt/nas": "/Volumes/nas" }`; `PUT /api/cluster/nodes/n1` resolves.
- **Steps:**
  1. Click "Edit mounts"; click add row; leave row 2 "from" filled with " smb://nas/x " and "to" blank; add row 3 with both " a " / " b ".
  2. Remove row 1 with its "×" button (`machines.pathMap.removeRow`).
  3. Save. Then Edit again and click Cancel.
- **Expected:** `PUT /api/cluster/nodes/n1` body `{ path_map: { "a": "b" } }` (trimmed, blank-half row dropped). Saving with every row removed sends `{ path_map: {} }` (empty map, not `null` — check this is meant; `null` is the reset). Cancel restores the read-only list without a request.

### MCH-19 — Storage server: blank name ignored, address parsing, delete, failure

- **Priority:** P2
- **Category:** Validation
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; home with `storage`; servers `[ { "name": "home-nas", "addresses": ["10.0.0.2"], "char_map": "none" } ]`.
- **Steps:**
  1. Click "Add a server"; leave "Name used in paths" blank; Save.
  2. Name " nas2 ", addresses "10.0.0.3, nas2.local 10.0.0.4,,", char map "sfm"; Save.
  3. Click "Remove home-nas".
  4. Make `PUT` reject `ApiError` 400 `{ detail: "bad address" }` and save again.
- **Expected:** Step 1: no request, form stays. Step 2: `PUT /api/cluster/storage-servers/nas2` `{ addresses: ["10.0.0.3", "nas2.local", "10.0.0.4"], char_map: "sfm" }`, list reloads, form closes. Step 3: `DELETE /api/cluster/storage-servers/home-nas` with no confirm step (destructive without confirmation — flag). Step 4: toast "bad address"; form stays open with values.

### MCH-20 — Node status badges: online, offline, draining, paused, revoked, rejected

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; nodes: `online: false` + `last_seen_at` 2 h ago; `drain: true`; `state: "rejected"`; `full_node: false`.
- **Steps:**
  1. Open `/admin/machines`.
- **Expected:** "Offline" with "last seen" text; "Draining" (warn); "Rejected" with only a "Remove" button (no drain/revoke, no stage table); worker-only badge (`machines.workerOnly`). Online cards show no last-seen text.

### MCH-21 — Live status badge and socket unavailable

- **Priority:** P2
- **Category:** Error
- **Covered by existing test?** Partial — `ClusterViews.test.tsx` "says when the live view cannot connect".
- **Preconditions / data:** admin; `FakeSocket` that (a) opens, (b) closes with an error and reconnects, (c) never connects (`WebSocket` constructor throws).
- **Steps:**
  1. Open `/admin/machines`.
- **Expected:** Badge `role="status"`: (a) "Live", (b) "Reconnecting…", (c) "Live view unavailable"; live section says "No machine is connected to the hub." or `cluster.live.unavailable`; jobs counts show "Jobs running: –". The approvals part still works from REST.

### MCH-22 — Tabs switch panels; "open logs" jumps to the Logs tab

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** Partial — `ClusterViews.test.tsx` clicks tabs; no keyboard or Back check.
- **Preconditions / data:** admin; socket open with one process.
- **Steps:**
  1. Click each tab: Jobs, Queues, Library, Logs, LLM, Relay, Machines.
  2. On Machines, use a live machine's "open logs" action.
  3. Press browser Back.
- **Expected:** Only the active panel's content is mounted; `aria-selected="true"` on the active tab. Step 2: Logs tab selected with that target. Step 3: Back leaves `/admin/machines` entirely (tab is not in the URL; a deep link to a tab is impossible — note as a gap, e.g. `?tab=logs`).

### MCH-23 — Jobs link goes to the filtered queue page; Back returns

- **Priority:** P2
- **Category:** Navigation
- **Covered by existing test?** No (e2e-only)
- **Preconditions / data:** full app; admin; machine "Studio Mac" id `n1`.
- **Steps:**
  1. Click the machine's jobs link (`machines.jobs.view`).
  2. Press Back.
  3. Click a machine web link (`WebLink`).
- **Expected:** Step 1: `/queue?host=n1&name=Studio%20Mac`, queue page filtered. Step 2: Machines page renders again (fresh load, skeleton then data). Step 3: opens in a new tab (`target="_blank"`, `rel="noopener noreferrer"`); only http(s) URLs are links.

### MCH-24 — Render crash shows route error fallback; Back still works

- **Priority:** P1
- **Category:** Error
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** full app in `MemoryRouter` with history `["/admin/health", "/admin/machines"]`; admin. Force a throw with `GET /api/cluster` -> `{ "home": null, "nodes": [] }` (MCH-08) or a node with `workers: [{ "stages": null }]` (MCH-09).
- **Steps:**
  1. Navigate to `/admin/machines`.
  2. Press browser Back; then click sidebar "Machines" with good data; then "Logs".
- **Expected:** A route-level error fallback with shell and nav, not a blank page. Back renders `/admin/health` without a reload. After the crash, the 5 s poll, relay poll and socket are torn down (no requests from the dead page). Nav links work.

### MCH-25 — Polling and socket stop after leaving the page

- **Priority:** P2
- **Category:** Happy path
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** admin; fake timers; `FakeSocket`.
- **Steps:**
  1. Open the page; advance 10 s.
  2. Navigate to `/library`; advance 20 s.
- **Expected:** Step 1: `/api/cluster` and `/api/cluster/relay` each polled. Step 2: no more `/api/cluster*` calls; socket unsubscribed/closed (or kept shared but with no topics from this page); no React "state update on unmounted component" warnings.

### MCH-26 — Arabic: RTL, Arabic machine names, LTR codes and paths

- **Priority:** P2
- **Category:** RTL
- **Covered by existing test?** Yes (Machines.cases.test.tsx)
- **Preconditions / data:** `localStorage["mkt:locale"] = "ar"`; home name "خادم البيت"; pending node "حاسوب المكتب"; path map `{ "smb://nas/أفلام": "/Volumes/أفلام" }`.
- **Steps:**
  1. Open `/admin/machines`; look at the pending code input, path map and stage table.
- **Expected:** `dir="rtl"`; h1 "الأجهزة"; Arabic names wrapped in bidi isolates (no reordering of surrounding labels such as "Code shown on {name}"); code input `dir="ltr"`; path rows `dir="ltr"`; time-left and counts in Arabic digits; stage table columns mirrored.

### MCH-27 — Phone width: cards, stage table and tabs fit

- **Priority:** P3
- **Category:** Responsive
- **Covered by existing test?** No
- **Preconditions / data:** browser e2e at 375x812; fixture plus a node with 12 stages and a 120-char web URL.
- **Steps:**
  1. Open `/admin/machines` (via "More"); scroll the tab list.
- **Expected:** No horizontal page scroll; tab list scrolls or wraps; stage table scrolls inside `.mkt-mc-table-wrap`; long URLs and paths wrap; dialogs fit.

### MCH-28 — Light and dark theme

- **Priority:** P3
- **Category:** Theme
- **Covered by existing test?** No
- **Preconditions / data:** `data-theme="light"` / `"dark"`; one node per status; storage check with ok/error.
- **Steps:**
  1. Open the page in each theme; open the Relay tab.
- **Expected:** Badges, `.mkt-mc-check--ok/--error`, skeleton and chips readable; axe contrast passes; styles in `machines.css` / `cluster.css` use tokens, not fixed colours.

### MCH-29 — Keyboard: tabs arrows (incl. RTL), dialogs trap focus, live regions

- **Priority:** P3
- **Category:** Accessibility
- **Covered by existing test?** Partial — axe runs in `Machines.test.tsx` and `ClusterViews.test.tsx`.
- **Preconditions / data:** admin; fixture.
- **Steps:**
  1. Focus the "Machines" tab; press ArrowRight, End, Home. Repeat in Arabic.
  2. Open the Revoke dialog; Tab around; Escape.
  3. Trigger a wrong code.
- **Expected:** Step 1 LTR: ArrowRight moves to "Jobs". In RTL ArrowRight should move to the previous (visually right) tab; DS `Tabs` does not mirror arrows (`design-system/components/Tabs/Tabs.tsx`) — likely bug. Step 2: focus trapped, Escape closes, focus back on "Revoke". Step 3: error announced (`role="alert"`). Stage selects have names like "Pull from transcribe on Studio Mac".
