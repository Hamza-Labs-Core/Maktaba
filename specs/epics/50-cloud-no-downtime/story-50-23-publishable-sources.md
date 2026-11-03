# Story 50.23 — Publishable sources: permissions, Creative Commons, own recordings; geo

> Epic 50 · Phase B · Depends on: 50.22, Epic 34.1 · Needed by: 50.24, the broadcasting gate

> **Not legal advice. Confirm with a lawyer before launch** (README §14.9).

## Why

The question: if a YouTube video is public and nobody pays to watch it, what is the legal issue
with re-broadcasting it? The practical answer (README §14.6, research §6.1):

- **Free to watch isn't a licence to re-broadcast.** Copyright covers copying a work and showing
  it to the public whether or not money changes hands.
- **YouTube's Terms** don't allow downloading and redistributing outside YouTube's player without
  the rights holder's permission.
- **Non-commercial use** helps a fair-use argument but doesn't make it safe, and Maktaba charging
  publishers for the tier makes the platform commercial.

A lot of the owner's kind of content **is** clearly allowed to be shared. So this story makes
the publisher's home air only what is marked **publishable**, and makes marking easy. It is a
**safety feature on the publisher's home**, applied under the publisher's control. It isn't
Maktaba vetting content: Maktaba Cloud stays an infrastructure provider with no curation
(README §14.6).

## Description

### 1. What makes an item publishable

An item is publishable if any of these holds. The reason is stored on the item as
`publishable_reason`.

| Reason | How it gets set | Shown to viewers |
|---|---|---|
| `own` | The publisher marks a collection, series or folder as **their own recordings** | — |
| `cc_by` | **Automatic at YouTube intake.** yt-dlp's info JSON has `license` (for example "Creative Commons Attribution license (reuse allowed)"), and the YouTube Data API has `status.license = creativeCommon`. Either one sets `cc_by` and stores the attribution (title, uploader, URL, licence). Rechecked when the item's metadata is refreshed. | **Attribution** in the guide entry and on the watch page: "{title} by {uploader}, CC BY, {link}" |
| `source_permission` | The publisher records that a **source** allows sharing, once per source: a YouTube channel id, a playlist id, or a speaker (a person in the library's metadata). Each record holds a **proof link** (where the source says so: a video description, an About page, a website), an optional screenshot, a note, and who recorded it and when. Every item from that source is then publishable. | optional credit line |
| `cleared` | A one-off item the publisher marks with a proof link (the same fields, for a single item) | — |

- **Quran recitations** are treated like any other source. The Qur'an's text has no copyright,
  but a specific recording belongs to its reciter or label. A recitation item is publishable when
  its reciter or label has a `source_permission`, when it is `cc_by`, or when it is `own`. In the
  library's Quran metadata the reciter is a source, so one permission covers a reciter's
  recordings. If a label is known, the permission names the label.
- **Not publishable by default:** everything else, including YouTube items under the standard
  licence from a source with no recorded permission.
- **A source's permission can be withdrawn.** The publisher removes it, or a takedown (50.24)
  removes it. Its items stop being publishable at once (§3).

### 2. Where it lives

Everything is on the publisher's home, in the library database:

- `publishable_sources (id, kind channel|playlist|speaker|label, ref, proof_url, proof_file,
  note, recorded_by, recorded_at, revoked_at, revoked_reason)`;
- `items.publishable_reason`, `items.cc_attribution`.

The cloud receives none of it. When the channel's guide is published, each programme carries
only its attribution, if it needs one. A takedown arrives as an instruction to the home (50.24).

### 3. The scheduler, while a channel is published

- **The default (`broadcast.publishable_only = true`).** When a channel is published, the
  scheduler (Epic 27) builds its schedule **from publishable items only**. A channel may mix
  sources in its rules; the non-publishable ones are skipped while it is published.
  - LAN viewers see the same channel.
  - Unpublishing restores the full rules at the next horizon top-up.
- **Not enough publishable content.** If fewer than 6 of the next 24 hours can be filled,
  publishing is refused (50.22 check 3). While the channel is published, gaps are filled with the
  channel's filler (Epic 27.10), as when a rule runs dry.
- **An item loses its mark after scheduling** (a takedown, a withdrawn permission, or a CC
  licence change at refresh). Epic 34.3's slate plays in its slot ("Not available online", the
  next programme's title), and the next horizon top-up replaces it.
- The setting can be turned off per home (`broadcast.publishable_only = false`) only by typing a
  confirmation. The Broadcast page then marks each such channel "Not limited to publishable
  items". The default and the recommendation stay on.

### 4. The Broadcast page helps

- **Per channel:** the publishable share for the next 7 days, plus the top sources that would
  add the most hours if marked, each with a **Record permission** button. The button pre-fills
  the source and opens its YouTube About page or the description link, so the publisher can paste
  the proof link.
- **Library:** a "Publishable" filter and badge on items and sources. CC BY items show their
  attribution.
- **Bulk:** mark a folder or series `own` in one action.

### 5. Geo rules

- Per channel, an optional **allow** or **deny** list of ISO countries. It is sent to the edge
  with the channel's access settings and checked against `CF-IPCountry` at play and at every
  playlist.
- A **Maktaba-wide deny list** (`broadcast_geo_deny`, set by service admins; sanctions only by
  default) applies to every channel.
- A viewer outside gets `451 geo_blocked` and `broadcast.geoBlocked`. The publisher sees blocked
  attempts by country in analytics (50.28).

## Acceptance criteria

- **Given** a YouTube video whose yt-dlp info JSON says the Creative Commons Attribution licence,
  **when** it is ingested,
  **then** it is `cc_by` with its attribution, and it shows the attribution in the public guide
  and on the watch page.
- **Given** a source `UCxxxx` with a recorded permission and a proof link,
  **when** the scheduler builds a published channel that includes that source,
  **then** its items are scheduled; items from sources with no permission are skipped.
- **Given** a published channel whose rules mix publishable and non-publishable sources,
  **when** the LAN player tunes it,
  **then** it shows the same schedule as the public guide.
- **Given** a published channel and an item scheduled for tonight,
  **when** its source's permission is withdrawn,
  **then** the item's slot plays the slate, and the next top-up replaces it with publishable
  items.
- **Given** a Quran recitation item whose reciter has no permission and which isn't CC or own,
  **when** the channel is published,
  **then** it isn't scheduled.
- **Given** a channel limited to `SA, EG`,
  **when** a viewer in `DE` opens it,
  **then** the edge answers `451 geo_blocked`.
- **Given** a user who isn't an admin,
  **when** they record a source permission,
  **then** the API answers `403`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (pipeline, pytest) | info JSON fixtures: CC BY, standard, missing, odd casing | intake | `cc_by` + attribution / none / none / `cc_by` |
| T02 | unit (pipeline) | Data API fixture `status.license` | refresh | `creativeCommon` → `cc_by`; a change to `youtube` clears it |
| T03 | unit (Go, scheduler) | rules mixing sources, `publishable_only` on and off | build 24 h | publishable only when on; full rules when off |
| T04 | unit | a permission withdrawn mid-horizon | slot | slate in the slot; replaced at the next top-up |
| T05 | unit | Quran reciter as a source | permission on, off | recitations scheduled / skipped |
| T06 | unit (edge) | geo allow and deny, Maktaba deny | play from each country | 200 / 451 as expected |
| T07 | integration | home API | record, list and withdraw permissions; bulk `own` | stored on the home; nothing sent to the cloud (assert no call) |
| T08 | role test | home API | non-admin writes | `403` |
| T09 | Arabic | new keys | strings check | Arabic in every app catalog |
| T10 | rehearsal (CI, 50.29 `broadcast-publishable`) | home stack with mixed fixtures | publish, withdraw a permission | the guide holds only publishable programmes; the slate on withdrawal |

## Edge cases

- **A CC BY video that is in fact someone else's** (a re-upload marked CC). The `cc_by` reason
  stays, but a takedown (50.24) withdraws it, and a source permission can't override a takedown on
  that item.
- **A source that allows sharing "with credit".** Record a credit line on the permission; the
  guide and the watch page show it, like CC attribution.
- **A source that allows sharing but "not for commercial use".** The Broadcast page warns that
  Maktaba charges for its infrastructure and that the source's wording should be checked. The
  publisher decides; the item is publishable only if they record the permission anyway.
- **A playlist permission and a video from that channel outside the playlist.** Only the
  playlist's items are covered.
- **Metadata missing** (an old item with no info JSON). It isn't publishable until marked.
- **Many publishers recording the same source.** Each home keeps its own record; there is no
  shared list (README §14.10, question 14).

## User-facing strings

| Key | EN | AR |
|---|---|---|
| `broadcast.publishable` | Publishable | قابل للنشر |
| `broadcast.publishable.reason.own` | Your own recording | تسجيلك الخاص |
| `broadcast.publishable.reason.ccBy` | Creative Commons (CC BY) | المشاع الإبداعي (CC BY) |
| `broadcast.publishable.reason.permission` | The source allows sharing | المصدر يسمح بالمشاركة |
| `broadcast.publishable.help` | Only content you may share airs on a published channel: your own recordings, Creative Commons videos, and sources that allow sharing. Free to watch on YouTube isn't the same as free to re-broadcast. | لا يُعرض على القناة المنشورة إلا المحتوى المسموح بمشاركته: تسجيلاتك الخاصة، ومقاطع المشاع الإبداعي، والمصادر التي تسمح بالمشاركة. كون المقطع مجانيًا للمشاهدة على يوتيوب لا يعني أنه مسموح بإعادة بثه. |
| `broadcast.recordPermission` | Record permission | سجّل الإذن |
| `broadcast.permission.proof` | Link to where the source allows sharing | رابط الموضع الذي يسمح فيه المصدر بالمشاركة |
| `broadcast.share` | {percent}% of the next 7 days is publishable | {percent}% من الأيام السبعة القادمة قابل للنشر |
| `broadcast.attribution` | {title} by {author}, CC BY | {title} من {author}، رخصة CC BY |
| `broadcast.notEnough` | Not enough publishable content to fill this channel. Record permissions for its sources, or add your own recordings. | لا يوجد محتوى قابل للنشر يكفي لملء هذه القناة. سجّل أذونات مصادرها، أو أضف تسجيلاتك الخاصة. |
| `broadcast.geoBlocked` | This channel isn't available in your country. | هذه القناة غير متاحة في بلدك. |
| `broadcast.nonCommercialWarning` | This source asks for non-commercial use. Maktaba charges for hosting, so check the source's wording before you record permission. | يطلب هذا المصدر استخدامًا غير تجاري. تتقاضى مكتبة رسومًا مقابل الاستضافة، لذا تحقق من صيغة المصدر قبل تسجيل الإذن. |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/discovery/ytdlp_sidecars.py`, `integrations/youtube.py` | Read `license`; set `cc_by` and the attribution; refresh. |
| `pipeline/src/maktaba_pipeline/schedule/` (TV schedule builder) and `streaming/internal/channel/` (scheduler side) | `publishable_only` while published; slate on a lost mark. |
| `api/internal/broadcast/publishable.go` (new) | Sources API, bulk `own`, share computation. |
| `api/migrations/` (next free slot) | `publishable_sources`, `items.publishable_reason`, `items.cc_attribution`. |
| `web/src/pages/LiveTv/Broadcast*.tsx`, the library filters, `web/src/i18n/en.json`, `ar.json` | Share, record permission, badges. |
| `web/packages/broadcast-edge/src/geo.ts` | Geo checks. |
| `cloud/internal/handlers/admin/broadcast_geo.go` (new) | The Maktaba-wide deny list (service admins only). |
| `docs/broadcasting.md` | "What can I broadcast?" in English and Arabic, with the not-legal-advice note. |

### Tests

- T01–T09 in the pipeline (`uv run --extra dev pytest`), streaming, API, web and edge unit jobs.
- **CI only:** rehearsal `broadcast-publishable` (50.29).

### Rollout

1. Intake detection on (harmless: it only marks items).
2. `publishable_only` ships with 50.22, default on.
3. The owner's library: run the share report, record permissions for known permissive sources,
   then publish.

### Rollback

- The intake marks are data; turning detection off leaves them in place.
- `publishable_only` can be turned off per home only by a typed confirmation. It isn't a global
  kill.

### Dependencies

50.22 (the Broadcast page, publishing). Epic 34.1 (exclusion and slate model), Epic 27
(scheduler, filler).
