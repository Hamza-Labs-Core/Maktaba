# Plan 32.13 — Simulcast to YouTube, Facebook and other RTMP destinations — implementation

> Implementation plan for [story-32-13](story-32-13-simulcast.md).
> Shipped in slot **0126** (`0126_tv_simulcast.sql`): 0125 (`tv_remote`) was already merged.
> Design: [design.md](design.md) §7.9.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Simulcast uses **Cloudflare live outputs** on the channel's existing push input; Maktaba never pushes to YouTube/Facebook itself. | The home uploads each channel once (≈ 6.5 Mb/s) whatever the number of destinations; no second encode or second upload. |
| D2 | Outputs are created when a window starts and **deleted** when it ends, never left on an idle input. | Inputs are pooled and reassigned between channels (32.7 D1); a lingering output would send the next channel to the destination. |
| D3 | A push has a set of **holds** (`viewers`, `simulcast`); the idle timeout only counts when no hold remains. | One push per channel serves remote viewers and simulcast alike (R7.3). |
| D4 | Destination stream keys live in `home_secrets` (0119), sent to Cloudflare only when an output is created. | Same rule as every secret in this epic (README Q7); Cloudflare holds the key only while the output exists. |
| D5 | Schedules resolve against the channel's timeline (`channel_programs`) on the home, with lead/tail margins. | The timeline already says when a programme, series or live slot airs; no second clock. |
| D6 | A separate cap in output-hours (`tv_simulcast.max_hours_per_month`), in addition to the budget. | One always-on output costs ≈ $43/month in delivered minutes; a cap the admin reads in hours is clearer than dollars. |
| D7 | Event metadata on YouTube/Facebook (title, description, privacy) is out of scope. | Cloudflare forwards only the media; the platform APIs need OAuth apps per platform — a later story if wanted. |
| D8 | Status per output is Maktaba's own state plus the input's status; per-output health from Cloudflare only if the spike finds it (README Q13). | The documented output object has `uid`, `url`, `streamKey`, `enabled` only. |

## 1. Files

**Create**
- `shared/db/migrations/0124_tv_remote.sql` (+ `.sqlite.sql`) — the tables in §2 (or `0125_tv_simulcast.sql` if 0124 is merged); MANIFEST row; `pipeline/tests/db/test_tv_simulcast_migration.py`
- `api/internal/tvremote/simulcast.go` — destinations, attachments, window resolution, holds, output create/delete, cap accounting
- `api/internal/tvremote/schedule.go` — rules → windows (weekly windows in the channel's time zone, programme/series/live-slot matches from `channel_programs`, merge, lead/tail)
- `api/internal/tvremote/cf/outputs.go` — `CreateOutput(input, url, key, enabled)`, `UpdateOutput(input, output, enabled)`, `DeleteOutput(input, output)`, `ListOutputs(input)`
- `api/internal/handlers/tvremote/simulcast.go` — the admin API in §3
- `web/src/pages/Admin/LiveTV/Simulcast.tsx` (+ test) — destinations list and form, the channel's attachments, the rights dialog, per-output status and start/stop
- tests with the shared fake Cloudflare server (`api/internal/tvremote/cftest`, extended with outputs)

**Modify**
- `api/internal/tvremote/push.go` — holds; the idle timeout counts only without holds; rotation and failover keep outputs (same input)
- `api/internal/tvremote/budget.go` — simulcast minutes in the estimate; the cap; thresholds
- `pipeline/src/maktaba_pipeline/schedule/tv_settings.py` — rules section `tv_simulcast` (README "Settings"), then regenerate `api/internal/handlers/rules/schema.json`
- `web/src/pages/Admin/LiveTV.tsx` — the Simulcast section; channel row → "Simulcast" action
- `web/src/i18n/en.json`, `ar.json`

## 2. Tables

```sql
CREATE TABLE IF NOT EXISTS tv_simulcast_destinations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('youtube','facebook','rtmp')),
  ingest_url TEXT NOT NULL,                -- rtmp:// or rtmps://; the key is in home_secrets
  key_secret TEXT NOT NULL,                -- home_secrets.name
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS tv_simulcast_attachments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  destination_id UUID NOT NULL REFERENCES tv_simulcast_destinations(id),
  mode TEXT NOT NULL CHECK (mode IN ('always','schedule')),
  rules JSONB NOT NULL DEFAULT '[]'::jsonb, -- [{kind: window|programme|series|live, …}]
  enabled BOOLEAN NOT NULL DEFAULT false,
  manual TEXT CHECK (manual IN ('start','stop')), manual_until TIMESTAMPTZ,
  rights_confirmed_by UUID, rights_confirmed_at TIMESTAMPTZ,
  UNIQUE (channel_id, destination_id));
CREATE TABLE IF NOT EXISTS tv_simulcast_outputs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  attachment_id UUID NOT NULL REFERENCES tv_simulcast_attachments(id) ON DELETE CASCADE,
  push_id UUID REFERENCES tv_remote_pushes(id),
  input_id UUID REFERENCES tv_remote_inputs(id),
  cf_output_uid TEXT,
  state TEXT NOT NULL CHECK (state IN ('scheduled','starting','live','stopping','off','errored')),
  window_start TIMESTAMPTZ, window_end TIMESTAMPTZ,
  started_at TIMESTAMPTZ, ended_at TIMESTAMPTZ, minutes NUMERIC NOT NULL DEFAULT 0,
  error TEXT);
ALTER TABLE tv_remote_pushes ADD COLUMN IF NOT EXISTS holds TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE tv_remote_usage_daily ADD COLUMN IF NOT EXISTS simulcast_minutes NUMERIC NOT NULL DEFAULT 0;
-- maktaba_worker: nothing on the three simulcast tables (the home API is the only reader).
```

(When the tables go into 0124 itself, the two `ALTER`s become columns of the `CREATE`s.)

## 3. API (admin, on the home)

```
GET    /api/tv/simulcast                              destinations, attachments, outputs (status), month's output-hours vs cap
POST   /api/tv/simulcast/destinations                 {name, kind, ingest_url, stream_key}
PUT    /api/tv/simulcast/destinations/{id}            {name?, ingest_url?, stream_key?}
DELETE /api/tv/simulcast/destinations/{id}            409 destination_in_use while simulcasting
GET    /api/channels/{id}/simulcast                   the channel's attachments + next windows
PUT    /api/channels/{id}/simulcast                   [{destination_id, mode, rules, enabled, rights_confirmed?}]
                                                      409 rights_confirmation_required
POST   /api/tv/simulcast/outputs/{attachment_id}/start  manual start until the next window boundary
POST   /api/tv/simulcast/outputs/{attachment_id}/stop
```

## 4. Controller (code shape)

```go
// every reconcile (2 s, design §7.1)
for _, a := range c.store.EnabledAttachments(ctx) {
    want := a.Mode == Always || c.sched.InWindow(a, now) || a.Manual == Start
    want = want && a.Manual != Stop && !c.budget.SimulcastCapped() && c.ready()
    o := c.store.Output(a)
    switch {
    case want && o.Off():  c.holdPush(a.ChannelID, "simulcast"); c.store.Starting(o)
    case want && o.Starting() && c.pushLive(a.ChannelID):
        key := c.secrets.Get(a.Destination.KeySecret)
        uid, err := c.cf.CreateOutput(ctx, c.push(a).InputUID, a.Destination.IngestURL, key, true)
        c.store.Live(o, uid, err)
    case !want && (o.Live() || o.Starting()):
        c.cf.DeleteOutput(ctx, o.InputUID, o.CFUID); c.store.Off(o); c.releaseHold(a.ChannelID, "simulcast")
    }
}
```

Minutes per output are accrued from `started_at` while `live` (Cloudflare analytics give the
input's delivered minutes, which include the outputs; the per-output split is Maktaba's own).

## 5. Tests

Fake Cloudflare with outputs (create, delete, list; scripted errors); fake clock; window
resolution (DST, midnight, merges); holds vs idle timeout; failover and rotation keep the input
and outputs; cap and budget thresholds; rights confirmation; migration up/down (Postgres and
SQLite); web panel (vitest).

## 6. Rollout

Behind `tv_simulcast.enabled` (off). First test: one RTMP destination on a private YouTube event,
a one-hour window on a channel the owner holds the rights to; watch the output state, a forced
failover and a rotation (with `max_push_hours = 1`).

## 7. As shipped (notes)

- **Slot 0126.** `tv_simulcast_outputs` also keeps `channel_id`, `destination_id` and the
  Cloudflare `input_uid`, with `attachment_id ON DELETE SET NULL`: an output whose attachment
  or channel is deleted can still be deleted at Cloudflare (EC4). `holds` is `jsonb`
  (`["viewers","simulcast"]`) rather than `text[]`, so the store's SQL stays portable to the
  SQLite twin.
- **Order in a reconcile pass.** The simulcast step runs before the pushes' steps: it decides
  which attachments want an output, sets the channels' `simulcast` holds (the idle timeout
  and the rotation read them in the same pass), requests a push for a held channel that has
  none (the tune gates minus the per-user ones: remote failed, `max_channels`, the storage
  guard), then creates or deletes outputs on live pushes.
- **Rotation.** A held push past `max_push_hours` rotates (`end_reason = rotate`) like one
  with viewers, and its successor starts on the same input even with no viewer. The outputs
  stay on the input (no `POST …/outputs`); the output row's `push_id` moves to the successor.
  The destination sees one gap: from the old broadcast's end until the successor is live
  (Cloudflare ends a broadcast `timeoutSeconds` = 30 s after its publisher leaves, then the
  recording is deleted and the next push starts), so tens of seconds rather than "a few".
- **Input swap.** A retry on a fresh input (an `ERR_*` error that needs one, 32.7 AC11) deletes
  the old input at Cloudflare, and its outputs with it. The output row keeps its identity and
  minutes; once the new push is live the controller creates the output again on the new input
  (one more `POST …/outputs`, the destination sees a reconnect). A retry on the same input
  keeps the output.
- **Idle inputs.** Outputs are deleted before an input goes idle (the end of a push without a
  successor, a failed push), checked again when an idle input is handed to a channel, and the
  startup sweep deletes whatever Cloudflare lists on an input without a running push.
- **Status (Q13).** While an output is live its input's outputs are listed every 15 s; when
  the answer carries a `status`/`state` field it is stored (`cf_status`) and shown, else the
  page says "sent; the destination's state is shown on the platform". An input `ERR_*` error
  (polled or from the webhook) shows on its outputs as `errored`.
- **Cost.** Output minutes are Maktaba's own (`started_at` to `ended_at` while live) and are
  added to the month's estimate on top of the analytics, so the estimate errs high if
  Cloudflare's per-video analytics already include output minutes (for the spike to show).
  `tv_remote_usage_daily.simulcast_minutes` keeps the per-day split.
- **Cap and budget.** At 100 % of `max_hours_per_month` no output starts and `always` outputs
  stop at once (event `tv_simulcast_capped`); scheduled and manually started ones finish
  their window. With the budget used up (`block_new`) no output starts and running ones keep
  going (an `always` output then runs until the cap). Thresholds: events
  `tv_simulcast_cap_80` / `tv_simulcast_cap_100` and `app_settings.tv_simulcast.cap_notice`.
- **Manual start/stop.** Stored as `manual` + `manual_until` (the next window boundary; none
  for `always`, which Start/Stop toggle).
- **Spike (README Q13 and the real account).** To confirm: whether the output object reports a
  connection status; that outputs survive a broadcast end/restart on the same input; the
  reconnect time a destination sees on rotation; whether output minutes appear in
  `streamMinutesViewedAdaptiveGroups`; YouTube's and Facebook's acceptance of the forwarded
  stream (codec, keyframes, RTMPS to Facebook).
