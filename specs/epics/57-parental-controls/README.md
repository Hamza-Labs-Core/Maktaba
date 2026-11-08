# Epic 57: Parental controls on iPhone and iPad, on Apple's Screen Time

> **Status:** spec (2026-11-08). **Platforms:** the Apple Player on iPhone and iPad, and the
> home's API and streaming service. Apple TV only where it falls out (story 57.11). Android, the
> web, the desktop Player and the Mac Player are a later epic (§4). **Builds on:**
> [Epic 10](../10-auth-security/README.md) (sessions, signed URLs),
> [Epic 25](../25-cloud-relay/README.md) and [Epic 50](../50-cloud-no-downtime/README.md) (the
> relay and linked sign-ins), [Epic 26](../26-content-intelligence/README.md) (classification,
> the context card), [Epic 27](../27-live-channels/README.md) (channels and the guide),
> [Epic 45](../45-libraries-ui/README.md) (library ACLs; its shelved story 45.23 asked for this),
> [Epic 48](../48-native-players/README.md) (the native Players),
> [Epic 54](../54-plugins/README.md) (plugin panels and `set-field`) and
> [Epic 56](../56-tv-library-view/README.md) (the play queue). **Research:**
> [research.md](research.md). **Wiki:**
> [epic-57-parental-controls.md](../../../docs/wiki/epics/epic-57-parental-controls.md).

## 1. Why

Requirement: a parent must be able to hand a child an iPhone or iPad with Maktaba on it and know
the child sees only what the parent allows. That has to hold everywhere content can appear, on
the home network and through Maktaba Cloud, and no app, modified or not, may get around it.
Today the only limit is which libraries a user can read (`library_acl`). Epic 45 listed parental
controls as missing and shelved its profiles-with-a-PIN story (45.23).

Apple already gives parents most of the device side: Family Sharing child accounts, Screen Time
(time limits, downtime, app limits, Always Allowed), Content & Privacy Restrictions with movie and
TV rating limits per region, the App Store age gate and Ask to Buy. From iOS 26 an app can also
learn the child's age range (Declared Age Range) and read the movie and TV rating limit in force on
the device (ManagedSettings). Maktaba should not rebuild any of that. What Apple cannot do is
judge Maktaba's own content, which lives on the home and not in Apple's store. So the home rates
its content, and the home's API filters it for restricted accounts and for devices that tell it a
child is using them.

## 2. What exists, and the gaps

Checked on `main` at c4b6d541b (2026-11-08). Details and file paths are in [research.md](research.md).

### 2.1 The home

| Area | Today |
|---|---|
| Roles | two: admin (`users.is_admin`) and viewer; per-library read through `library_acl` (slots 0029, 0030, 0072) |
| Principal | `UserID, IsAdmin, Libraries, AccessAllLibraries, Source, CloudScope`; no device, profile or age (`api/internal/auth/principal/principal.go`) |
| Tokens | access JWT 15 min with `usr, lib[], is_admin, scp`; cloud tokens 5 min; refresh tokens carry an optional `device_id` (slot 0053) |
| Access checks | **no central check**: about 15 per-package library scopes (browse, collections, search, recommendations, series, play queue, streaming, channels, guide, libraries, websocket, panels, thumbnails) |
| Row cache | `browse/rowcache.go` caches Home and hub rows per library, on the stated rule that a row has no per-user part |
| Streaming | signed URLs (24 h cap) checked by `SignedURL` and `LibraryGuard`; **live channel streams `/stream/channel/{id}/…` and the HDHomeRun routes are not signed** |
| Channels | a channel with no library (`library_id` null) is visible to every signed-in user |
| Relay | forwards to the loopback API, so the home's own checks apply; linked sign-ins are play-scoped |
| Ratings | **no rating column anywhere.** Free-text `content_rating` exists in `videos.metadata` (written only by plugins), in `media_metadata_enrichment.mapped` (nothing writes it), in `plex_items.detail.contentRating` and in the guide's `title_snapshot.rating`. YouTube videos keep no age flag |
| Pairing | approving a code sign-in signs the device in as the approver; no approve-for-someone-else |

### 2.2 The Apple Player

| Area | Today |
|---|---|
| Targets | iOS 26.0, tvOS 26.0, macOS 15.0 (`apps/apple/project.yml`, `MaktabaKit/Package.swift`) |
| Entitlements | none on iOS or tvOS; Mac sandbox only |
| Parental, StoreKit, Family Controls, Declared Age Range | none |
| Downloads | none (`device_downloads` is a flag; no `AVAssetDownloadTask`) |
| AirPlay, Picture in Picture | yes (`AirPlay.swift`, `PictureInPicture.swift`) |
| Strings | String Catalogs per screen; `tools/apple/check-string-catalogs.py` fails on a key without Arabic |
| CI | `_apple-apps.yml`: MaktabaKit tests, catalog check, Player UI tests on throwaway simulators against `UITests/MockHome.swift` |

### 2.3 Gap list

- **G1** No way to mark an account as a child's, or to say who looks after it.
- **G2** No rating on any video, series, collection, channel, YouTube source or plugin's content.
- **G3** No single place where the home decides what a user may see, so a new filter would have to
  be added in 15 places and kept there.
- **G4** Unsigned live channel streams, HDHomeRun routes open to the LAN, and channels with no
  library visible to everyone.
- **G5** The Player ignores what Apple knows about the person holding the device.
- **G6** No way for a child to ask for something, and no way for a parent to see what was watched.
- **G7** Help says nothing about Screen Time.

## 3. Goals

1. **Restricted accounts.** An admin, or a guardian an admin names, makes a child's account
   restricted, with an age limit, allow and block lists, and a rule for unrated content. Only
   they can change it, and the API enforces that, with role tests.
2. **Content ratings.** Every video gets an age tier (All ages, 9+, 13+, 16+, 18+, or unrated)
   from what the home knows: Plex content ratings, guide ratings, YouTube's age limit, plugins,
   and what an admin sets on a library, collection, series, channel, YouTube source or video.
3. **One filter on the server, on every surface.** Browse, Home, search (titles, transcripts,
   suggestions), recommendations, Continue Watching, On Deck, collections, series, playlists, play
   queues, the guide and live streams, thumbnails and posters, plugin panels, a video by its ID,
   stream sessions, AirPlay (it plays the same signed URL), the websocket, GraphQL, and the cloud
   relay. A blocked item answers 404, the same as one that does not exist.
4. **Apple's signal.** The Player sends the home the age range from Declared Age Range and the
   movie and TV limit from Screen Time. The home applies the strictest of the account's policy and
   the device's signal. A signal can only tighten, never loosen.
5. **Apple's controls do the rest.** Time limits, downtime, app limits, Always Allowed, the App
   Store age gate and Ask to Buy stay Apple's. Maktaba behaves well under them and explains them
   in Help.
6. **Ask a parent.** A child can ask for a blocked item. A guardian approves it from their own
   Player, or types a parent PIN on the child's device. The PIN is checked by the home, with
   limits on tries.
7. **Activity.** A guardian sees what a child watched, what was blocked and what was asked for,
   kept on the home for 90 days.
8. **English and Arabic.** Every new iOS string ships in both, mirrored in Arabic.
9. **Privacy.** The home stores only the age band and where it came from, never a birth date.
   Nothing about a child leaves the home unless a guardian turns on notifications.

## 4. Non-goals

- **Android (phone, tablet, TV), the web, the desktop Player and the Mac Player.** They are a
  later epic. Even so, the home's filter applies to every client from the first build: a
  restricted account signed in on Android or the web simply does not get blocked items. Those
  clients just lack the restricted UI, Ask a parent and the Family screen.
- **No Maktaba time limits, schedules, bedtime or app-leave PIN.** Screen Time does these on the
  device, and better (story 57.09).
- **No profiles inside one account** (Plex Home's switch-user grid). A child has their own
  Maktaba account, as Apple's Family Sharing gives a child their own Apple Account.
- **No Family Controls entitlement in this epic.** The rating limit can be read without it
  (§6.3). Using Family Controls, DeviceActivity or shields would need Apple to approve
  `com.apple.developer.family-controls` for distribution, separately for each extension. That is
  kept as a later option (decision 6).
- **No StoreKit.** The iOS app sells nothing, so Ask to Buy has nothing to cover (story 57.09
  says what changes if it ever does).
- **No word-level screening of transcripts** (bad-word filters). Ratings come from metadata and
  from people.
- **No web filtering.** Safari's limits are Screen Time's.

## 5. Who does what: Apple and Maktaba

| Need | Apple (set by the parent in Settings > Screen Time, or Family) | Maktaba |
|---|---|---|
| A child's account | Family Sharing child account (Child Account from iOS 27) | a restricted Maktaba account, made by an admin or guardian (57.02) |
| How long, and when | Screen Time: app limits, downtime, Always Allowed, communication limits | nothing to build; saves progress and stops cleanly when shielded (57.09) |
| Installing the app | App Store age gate against the app's age rating; Ask to Buy | keeps its age rating honest; answers the questionnaire's In-App Controls (57.09) |
| Movie and TV limit | Content & Privacy Restrictions, Ratings For a region | reads it (`effectiveMaximumMovieRating`, `effectiveMaximumTVShowRating`) and sends it to the home (57.04) |
| Age | Declared Age Range: the guardian decides whether to share | asks once, sends the band to the home (57.04) |
| What content is suitable | nothing (Apple does not see the home's videos) | age tiers on the home's content (57.01), the server filter (57.03) |
| Asking for more | Ask to Buy, Ask for More Time | Ask a parent for one item, approved in the guardian's Player or with a PIN (57.06) |
| What was used | Screen Time reports (time per app) | Activity: titles watched, blocked, asked for (57.08) |

## 6. Design

### 6.1 Age tiers

One scale on the home, matching the App Store's own age tiers, so a parent sees the same numbers
on both:

| Tier | Shown as (en / ar) | Stored |
|---|---|---|
| All ages | All ages / لكل الأعمار | 0 |
| 9+ | 9+ / ‎+9 | 9 |
| 13+ | 13+ / ‎+13 | 13 |
| 16+ | 16+ / ‎+16 | 16 |
| 18+ | 18+ / ‎+18 | 18 |
| Unrated | Unrated / بلا تصنيف | null |

**Rounding rule:** content rounds **up** to the next tier (TV-14 is 16+, TV-Y7 is 9+), and a
limit rounds **down** (a TV-14 limit allows up to 13+). The two never meet in the middle, so a
mapping can only err toward hiding. The region tables (US MPA and TV Parental Guidelines, UK BBFC,
Saudi GCAM, Australia, Germany FSK, numeric `NN+`, Apple's 0–1000 scale) are one data file,
`shared/parental/ratings.json`, read by Go and Swift and checked by one set of test vectors in
both (57.01).

**Where a video's tier comes from**, first match wins:

1. an admin's tier on the video;
2. the strictest admin tier on anything containing it (series, collection, YouTube source,
   channel);
3. the strictest rating found in metadata: Plex `contentRating`, the guide programme's rating,
   YouTube `age_limit` (18 means 18+), a plugin's `content_rating` (`set-field`);
4. the library's default tier ("everything here is suitable for …"), set by an admin;
5. unrated.

The result is stored on the video (`videos.age_tier`, `age_tier_source`) and recomputed when any
input changes, so the filter is one indexed comparison and never re-derives in a query.

### 6.2 The effective limit for a request

```
account policy (restricted accounts)  ─┐
device signal (Declared Age Range,     ├─ strictest ─▶ effective limit + lists ─▶ the filter
  Screen Time movie/TV limit)          ─┘
```

- **Account policy** (`parental_policies`, one per restricted user): `max_tier`, `unrated`
  (`hide` default, or treat as a tier), `allow_only` (only allow-listed items, for young
  children), and allow and block entries on a library, collection, series, channel, YouTube
  source, plugin or video. Block always wins. Allow lets an item through above the limit and when
  unrated. Approved requests (57.06) become allow entries, optionally with an expiry.
- **Device signal** (57.04): the Player posts `{age_range: {lower, upper, declaration},
  movie_ceiling, tv_ceiling}` for its session. The home maps each to a tier limit (rounded down)
  and stores the strictest on the **refresh-token family** (the sign-in on that device). It is a
  ratchet: a looser signal later is recorded but not applied until a guardian clears it, or the
  account signs in afresh with its password.
- **Unrestricted accounts on a child's device.** If a parent signs into their own account on the
  child's iPad, the device signal still limits that sign-in. That is why the signal exists: the
  account cannot know which device it is on.
- **The ceiling for signal-only sign-ins** uses the home's `unrated_on_signal` setting (default
  `hide`), with the library default tiers of §6.1 filling most gaps.
- **When it applies.** The policy is read per request from an in-process cache, invalidated by
  `pg_notify('parental_policy')`, so a guardian's change applies at once, not when a 15-minute
  token expires. The device limit travels in the access token (`ac` claim), minted at refresh
  from the stored signal.

### 6.3 Apple's two signals

| | Declared Age Range | Screen Time movie and TV limit |
|---|---|---|
| API | `AgeRangeService.requestAgeRange(ageGates: 9, 13, 18)` (SwiftUI `\.requestAgeRange`) | `ManagedSettingsStore().effectiveMaximumMovieRating`, `effectiveMaximumTVShowRating` |
| Platforms | iOS, iPadOS, macOS 26+ (no tvOS) | iOS 15+, tvOS 26+ (read) |
| Entitlement | `com.apple.developer.declared-age-range` (a capability; no approval form) | none to read; observing changes needs `family-controls`, so the app re-reads on each foreground instead |
| Prompt | a system sheet; for a Family Sharing child the guardian chose always, ask, or never | none |
| Result | `.sharing(AgeRange)` with `lowerBound`, `upperBound`, `ageRangeDeclaration` (`.selfDeclared`, `.guardianDeclared`, `.confirmed`), or `.declinedSharing` | 0–1000 scale; 1000 is no limit |
| Maktaba's tier limit | under 9: All ages; 9–12: 9+; 13–17: 13+; 18+: none; declined: none | movie and TV each mapped with the device's region table, rounded down; 1000: none |

Gates 9, 13 and 18 are the three Apple allows. They give the App Store's 9+ and 13+ and the
adult line. 16+ is reached only through an account policy or a Screen Time limit. A
self-declared minor is honoured too, since anyone may restrict themselves. When the app is asked
for an age range in a region that requires it (`isEligibleForAgeFeatures`), it asks at first
launch. Elsewhere it asks once, at sign-in.

### 6.4 One filter, everywhere

A new package, `api/internal/parental`, gives:

- `Limit(ctx, p) → Effective` (tier, unrated rule, allow-only, lists, policy version);
- `SQL(e, alias) → (clause, args)`, the one predicate every content query adds next to its
  library scope;
- `Allowed(e, item) bool` for single objects (a video by ID, a channel, a panel's subject);
- `NotFound` answers that match a missing item byte for byte.

Every per-package scope helper (§2.1) calls it. A **surface registry** lists every route that
returns content. A router walk test fails when a mounted route that serves content is not in the
registry, so a new route cannot skip the filter (57.03, 57.13). The row cache keeps each row's
items with their tier and filters on read, so its per-library key stays correct.

Streaming: a stream session is made only after `Allowed`. A restricted sign-in's signed URLs last
at most 15 minutes and are renewed by the Player's `StreamProxy`, which re-checks the filter on
each renewal, so a block applies to a video already playing within 15 minutes. Live channel
streams gain signed URLs. HDHomeRun routes answer only the Plex servers they were added for
(57.10).

### 6.5 Data model and migrations

Slots **0216, 0217 and 0218 are planned.** Per `shared/db/migrations/MANIFEST.md` a slot is taken
when its migration merges: if `main` has moved past one when a story lands, the story takes the
next free integer and updates this table. Each has a `.sqlite.sql` twin.

| Slot | Story | File | Contents |
|---|---|---|---|
| 0216 | 57.01 | `0216_age_tiers.sql` | `videos` ADD `age_tier smallint null` check in (0, 9, 13, 16, 18), `age_tier_source text null` check `admin_video`/`admin_container`/`plex`/`guide`/`youtube`/`plugin`/`library_default`, `age_tier_admin smallint null`; index `(library_id, age_tier)`; `age_tier smallint null` on `libraries` (the default), `series`, `collections`, `channels`, `youtube_sources`; `youtube_videos` ADD `age_limit smallint null`; `channel_programs` ADD `age_tier smallint null` |
| 0217 | 57.02, 57.04 | `0217_parental_policies.sql` | `parental_policies` (user_id pk fk cascade, max_tier smallint, unrated text check `hide`/`tier_0`…`tier_18`, allow_only bool, version int, updated_by, updated_at); `parental_guardians` (child_id, guardian_id, pk both, check child ≠ guardian); `parental_rules` (id, user_id, effect `allow`/`block`, subject_kind `library`/`collection`/`series`/`channel`/`youtube_source`/`plugin`/`video`, subject_id text, expires_at null, created_by); `session_age_signals` (refresh_family_id pk, user_id, lower smallint null, upper smallint null, declaration text null, movie_ceiling int null, tv_ceiling int null, region text null, tier_limit smallint null, pending_looser jsonb null, observed_at); `home_settings` keys `parental.enabled`, `parental.unrated_on_signal`; check: a user with a policy is not `is_admin` |
| 0218 | 57.06, 57.08 | `0218_parental_requests.sql` | `parental_requests` (id, child_id, subject_kind, subject_id, status `pending`/`approved`/`denied`/`expired`, decided_by, decided_at, created_at); `parental_pins` (guardian_id pk, pin_hash argon2id, failed int, locked_until, updated_at); `parental_events` (id, child_id, kind `watched`/`blocked`/`asked`/`approved`/`denied`/`pin_locked`/`signal`, subject_kind, subject_id, at); retention index `(at)`; `parental_pins` joins `cluster_copy_excluded()` (starting from its newest definition on `main`) |

### 6.6 API

All under the signed-in user. Every role rule is enforced by the API, with a role test that tries
each route as no one, a viewer, a restricted child, the child's guardian, another guardian, an
admin, and a restricted child signed in through the cloud (57.13).

| Method and path | Who | Story |
|---|---|---|
| `PUT /api/videos/{id}/age-tier`, `PUT /api/{libraries,series,collections,channels}/{id}/age-tier`, `PUT /api/libraries/{id}/youtube/sources/{sid}/age-tier` `{tier \| null}` | admin | 57.01 |
| `GET /api/parental/ratings` (the tier scale and region tables, for the apps) | any signed-in user | 57.01 |
| `GET /api/parental/children` (the children this caller may manage) | admin, guardian | 57.02 |
| `PUT /api/users/{id}/parental` `{max_tier, unrated, allow_only}`; `DELETE` lifts it | admin; guardian of that user (cannot lift: admin only) | 57.02 |
| `POST /api/users/{id}/parental/guardians` `{guardian_id}`, `DELETE …/{gid}` | admin | 57.02 |
| `GET/POST/DELETE /api/users/{id}/parental/rules` | admin, guardian of that user | 57.02 |
| `POST /api/me/age-signal` `{age_range?, movie_ceiling?, tv_ceiling?, region}` → `{tier_limit, refresh: true}` | any signed-in user (their own sign-in) | 57.04 |
| `POST /api/users/{id}/parental/signals/{family}/clear` | admin, guardian | 57.04 |
| `GET /api/me/parental` → the caller's effective limit (no lists, for the UI) | any signed-in user | 57.05 |
| `POST /api/me/parental/requests` `{subject_kind, subject_id}` | a restricted user | 57.06 |
| `GET /api/parental/requests?status=pending`, `POST /api/parental/requests/{id}/{approve,deny}` `{expires_at?}` | admin, guardian of that child | 57.06 |
| `PUT /api/me/parental-pin` `{pin, password}` | admin, guardian | 57.06 |
| `POST /api/me/parental/unlock` `{guardian_id, pin, subject_kind, subject_id}` | a restricted user (the PIN is the guardian's) | 57.06 |
| `GET /api/users/{id}/parental/activity?cursor=` | admin, guardian of that user | 57.08 |
| `POST /api/pairing/approve` gains `as_user` (a child this guardian manages) | admin, guardian | 57.02 |
| `GET /api/capabilities` gains `parental: {restricted, tier_limit, can_manage}` | any signed-in user | 57.05 |

A restricted user is refused, with 403 `restricted`: personal access tokens, YouTube search and
import (`/api/search/youtube`, `import-youtube`), the XMLTV and M3U exports, plugin admin, and
anything an admin can do (they are never admins, so those already answer 403).

### 6.7 The Player on iPhone and iPad

```
Restricted child                                Guardian (Profile > Family)
┌──────────────────────────────┐                ┌──────────────────────────────┐
│ Home  Libraries  Live  Search│                │ Family                       │
│ (only allowed items; no      │                │  Sara   13+   2 requests  ›  │
│  YouTube search, no admin)   │                │  Omar   All ages          ›  │
│                              │                │ Requests                     │
│ Video blocked by a link:     │                │  Sara asks for "…"  ✓  ✕     │
│  This isn't available.       │                │ Parent PIN        Change  ›  │
│  [Ask a parent] [Enter PIN]  │                │ Notifications     Off     ›  │
└──────────────────────────────┘                └──────────────────────────────┘
Child page: Age limit [13+ ▾] · Unrated [Hide ▾] · Only allowed items [ ] ·
Allowed (list) · Blocked (list) · Devices (signals; Clear) · Activity ›
```

- A restricted child's Profile shows "Managed by <guardian>" and the limit. The Profile has no
  sign-out lock: switching accounts needs the other account's password, and a child's device
  limits any account signed in on it (§6.2).
- A link or a Continue entry to an item that is now blocked shows the blocked page, not an error.
- Arabic: every screen mirrors, tier labels read ‎+13 with an LRM so the plus sits on the correct
  side, and the guardian's lists sort by Arabic collation.

### 6.8 Offline, LAN and the relay

- **The filter lives in the home's API**, so the LAN, a direct address and the relay behave the
  same. A cloud sign-in is play-scoped (`ApplyCloudScope`) and keeps the account's restriction;
  57.13 tests it through the relay path.
- **No home, nothing new.** If the home cannot be reached, a child sees nothing new. The Player
  has no downloads today. When downloads come, the home refuses a download of an item the limit
  hides, and the Player deletes a downloaded item the next time the home says it is blocked.
- **The Player caches only what it was given.** Its caches are per account, and cleared on sign-out
  and when `GET /api/me/parental`'s version changes.
- **Ask a parent works on the LAN alone.** A request waits on the home until a guardian opens the
  Player. The PIN works on the LAN alone.

### 6.9 Privacy

- The home stores the age **band** and its source, per sign-in, deleted with the sign-in. Never
  a birth date (Apple does not give one either).
- Activity and requests stay on the home, kept 90 days (`parental_events` swept daily), and are
  visible only to the child's guardians and admins. Search terms are not recorded.
- Notifications to a guardian are **off by default**. When a guardian turns them on, a request
  sends a push through Maktaba Cloud and APNs that says only "A request is waiting" (in the
  guardian's language), never a title or a child's name.
- Crash reports and logs never carry a child's age band or titles (the redact rules gain the new
  fields, `RedactRules.swift`, and the Go log schema's redaction).

## 7. Faults and abuse: how a child might get around it

| # | Attempt | What stops it | Test |
|---|---|---|---|
| A1 | Open a blocked video by ID or link | `Allowed` on every by-ID route; 404 identical to a missing ID | 57.03 T02 |
| A2 | Its poster, trickplay, subtitles, chapters, context card, thumbnail by hash | same filter; `thumbVisible` gains the tier | 57.03 T03 |
| A3 | Search titles, transcript passages, suggestions, saved searches | filter in each query; suggestions rebuilt per tier | 57.03 T04 |
| A4 | Recommendations computed overnight, Home rows from the row cache | filtered on read; cache rows carry tiers | 57.03 T05 |
| A5 | A play queue made before the parent tightened the limit | window reads re-check (`checkVisible`) | 57.03 T06 |
| A6 | Websocket events naming other videos | events for restricted users are filtered or dropped | 57.03 T07 |
| A7 | GraphQL | replays REST handlers; covered by the registry walk | 57.13 T03 |
| A8 | Through the cloud relay or a cloud sign-in | same handlers; restriction survives `ApplyCloudScope` | 57.13 T04 |
| A9 | Reuse a stream URL after a block | restricted URLs ≤ 15 min, re-checked on renewal | 57.03 T08 |
| A10 | Unsigned live channel streams, HDHomeRun on the LAN | signed channel URLs; HDHomeRun answers only its Plex servers | 57.10 T03, T04 |
| A11 | A channel with no library | needs a tier; unrated hides for restricted users | 57.10 T02 |
| A12 | A modified app sends an adult signal, or none | signals only tighten; the account policy holds regardless | 57.04 T05 |
| A13 | Turn the age sharing off, then back on looser | ratchet on the sign-in; looser waits for a guardian | 57.04 T06 |
| A14 | Guess the parent PIN | home-checked, argon2id; 5 tries then 15 min, doubling to 24 h, per guardian and per child; guardians told | 57.06 T04 |
| A15 | Edit own policy, add self as guardian, approve own request | 403; a guardian cannot be restricted by the same chain; role tests | 57.02 T02 |
| A16 | Approve a device sign-in to get a parent's account onto a device | approval signs in as the approver; a restricted user approving gives a restricted sign-in; `as_user` only for a guardian's own children | 57.02 T06 |
| A17 | Make a personal access token, use YouTube search to reach outside content | 403 `restricted` | 57.03 T09 |
| A18 | A plugin panel shows other titles, or a plugin lowers a rating | panels need the plugin allowed; plugin ratings are below admin tiers and never loosen a container's | 57.03 T10, 57.01 T05 |
| A19 | Change the device clock for Screen Time | Apple's (Screen Time uses its own time); nothing Maktaba holds depends on the clock except request expiry, checked on the home | — |
| A20 | Safari to the home's web UI | the web gets the same filter (no restricted UI yet) | 57.03 T11 |
| A21 | AirPlay keeps playing on an Apple TV after Screen Time shields the iPad | residual: the receiver plays a URL already given; it expires within 15 minutes and the Apple TV has its own Screen Time; Help says so | 57.09 T03 |
| A22 | Delete and reinstall the app to drop the signal | the sign-in's ratchet is on the home; a new sign-in needs the account password, and the new sign-in sends a fresh signal | 57.04 T07 |

## 8. Phases

| Phase | Stories | Parallel? |
|---|---|---|
| 1. Home | 57.01 tiers, 57.02 accounts, 57.03 filter, 57.10 live TV | 57.01 and 57.02 first; 57.03 and 57.10 then, in parallel |
| 2. Signals and requests | 57.04 signal (API part), 57.06 requests and PIN (API), 57.08 activity (API) | yes |
| 3. Player | 57.04, 57.05, 57.06, 57.07, 57.08, 57.09 on iPhone and iPad; 57.11 Apple TV | 57.05 first |
| 4. Rollout | 57.12 existing homes, 57.13 OpenAPI, role tests and the bypass rehearsal | 57.13's registry walk lands with 57.03; the rest last |

## 9. Stories

| # | Story | Platforms |
|---|---|---|
| 57.01 | [Age tiers on the home's content](story-57-01-age-tiers.md) | API, pipeline |
| 57.02 | [Restricted accounts and guardians](story-57-02-restricted-accounts.md) | API |
| 57.03 | [One content filter on every surface](story-57-03-server-filter.md) | API, streaming |
| 57.04 | [The device's signal: Declared Age Range and the Screen Time rating limit](story-57-04-device-signal.md) | iOS, API |
| 57.05 | [The restricted Player on iPhone and iPad](story-57-05-restricted-player.md) | iOS |
| 57.06 | [Ask a parent, and the parent PIN](story-57-06-ask-a-parent.md) | API, iOS |
| 57.07 | [Family settings for guardians in the Player](story-57-07-family-settings.md) | iOS |
| 57.08 | [Activity for guardians](story-57-08-activity.md) | API, iOS |
| 57.09 | [Screen Time, Family Sharing and the App Store](story-57-09-screen-time.md) | iOS, Help |
| 57.10 | [Live TV under parental controls](story-57-10-live-tv.md) | API, streaming, pipeline |
| 57.11 | [Apple TV, where it falls out](story-57-11-apple-tv.md) | tvOS |
| 57.12 | [Turning it on in existing homes](story-57-12-existing-homes.md) | API, iOS |
| 57.13 | [OpenAPI, role tests and the bypass rehearsal](story-57-13-openapi-roles-rehearsal.md) | API, CI |

## 10. Test and rehearsal plan

Everything runs in CI on the jobs that already exist, and only for the code it touches (CI runs
only related jobs). Nothing runs against the live home.

| Layer | Where | What |
|---|---|---|
| Rating tables | Go unit and MaktabaKit XCTest on one vector file `shared/parental/vectors.json` | every region string and Apple scale value maps to the same tier in both languages, content up and limits down |
| Tier resolution | Go unit + Postgres (`internal/testport`) | the precedence of §6.1, recompute on each input change |
| Filter | Go Postgres tests per handler | each surface with a blocked, an allowed and an unrated fixture |
| Surface registry | Go router walk (57.13) | every mounted content route is registered and probed |
| Bypass rehearsal | new `parental-bypass` Go test target in the `api` job, Postgres and SQLite | the A1–A18 attempts of §7 against a seeded home, as a restricted child, directly and through the relay agent's loopback path |
| Roles | Go role tables (57.13) | seven principals × every route of §6.6 |
| Streaming | `streaming` Go tests | channel URLs signed; HDHomeRun address allowlist; restricted TTL |
| Player | MaktabaKit XCTest | signal mapping, ratchet handling, blocked states, PIN entry lockout display, request flow against `MockHome` |
| Player UI | `_apple-apps.yml` Player UI shards on throwaway iPhone and iPad simulators, English and Arabic | restricted child, guardian Family screens, Ask a parent; the age signal comes from a test double (`AgeSignalSource`), since the simulator cannot be a Family Sharing child |
| Strings | `check-string-catalogs.py` (with `--stringsdata`) | every new key has Arabic |
| Device check | a written checklist in story 57.04 for a real child device in a test family, run before the Phase 3 release tag | whether `effectiveMaximum…Rating` reflects the parent's own Content & Privacy setting (research.md, open item) |

## 11. Acceptance (the epic)

- A guardian makes a child's account 9+ on their own iPhone. On the child's iPad, the child sees
  no 13+ or unrated item on Home, Libraries, Search, Live TV or Continue Watching. The same items
  by ID, by search passage, by thumbnail and through the cloud answer 404.
- A parent's own unrestricted account, signed in on an iPad whose Family Sharing guardian shared
  an age range of 9–12, is limited to 9+ on that iPad and nowhere else.
- An iPad with Screen Time's TV limit at TV-PG gets 9+ (TV-PG rounds down to 9+).
- The child asks for a 13+ video. The guardian approves it from the Requests list, and the video
  plays on the iPad without a restart. Five wrong PINs lock the PIN for 15 minutes and tell the
  guardians.
- Screen Time downtime starting mid-video saves the position, and the video resumes there after
  downtime.
- A viewer, the child, or another guardian cannot change the child's policy (403, role tests).
- Every new string exists in English and Arabic, and each screen is checked mirrored.

## 12. Decisions (2026-11-08)

1. **Apple's controls for the device, Maktaba's for the content.** Time limits, schedules,
   bedtime and app-leave locks are Screen Time's. Rejected: Maktaba's own timers and an app-leave
   PIN, which duplicate Apple's and are weaker (a child can force-quit an app, not Screen Time).
2. **The filter is on the home**, and every client gets it, even ones whose UI comes later.
   Rejected: filtering in the app, which a modified app skips.
3. **A child has their own account.** Rejected: profiles inside one account with a PIN switcher
   (Plex Home style, Epic 45's shelved 45.23), which needs a second identity layer in every token
   and every table. Separate accounts reuse the roles, ACLs and tokens that exist.
4. **Device signals only tighten.** A device can say "a child is holding me", never "an adult is".
   So a modified app gains nothing, and the account policy is the floor. Rejected: trusting the
   device's age, which would let one patched app unlock everything.
5. **App Store tiers (All ages, 9+, 13+, 16+, 18+)** as the single scale, with region tables
   mapping in. Content rounds up, limits round down. Rejected: one region's labels (MPA), which
   mean little to an Arabic-speaking family.
6. **No Family Controls entitlement now.** Reading the effective limit needs none, and the app
   re-reads on foreground. Rejected for now: DeviceActivity or shields, which need Apple's
   approval for each extension and add nothing Screen Time does not already do. Revisit if Apple
   stops exposing the effective limit without it.
7. **Unrated is hidden from restricted accounts by default**, and an admin rates whole libraries
   with one choice each, so most homes rate everything in minutes (57.12). Rejected: unrated
   shown by default, which fails open.
8. **The parent PIN is a guardian's, checked by the home.** Rejected: a PIN kept on the device,
   which a child could read from a backup or brute-force offline.
9. **Blocked answers 404**, never 403, so a child cannot tell a blocked item from a missing one.
10. **Notifications off by default, and title-free.** Nothing about a child leaves the home
    unless a guardian opts in, and even then not what was asked for.

## 13. Questions only the owner can answer

None blocks the epic. Each has a default the stories use.

| # | Question | Default used |
|---|---|---|
| Q1 | Which rating systems should the home read, by region? | US MPA and TV Parental Guidelines, UK BBFC, Saudi GCAM, Australia, Germany FSK, plain `NN+`, and Apple's 0–1000 scale; unknown strings count as unrated and are logged |
| Q2 | What should a new restricted account start at? | 9+, unrated hidden, notifications off |
| Q3 | Should a library's default tier be set for existing libraries at upgrade? | no; the admin is asked once, per library, with All ages pre-selected for libraries whose content type is lectures or Quran (57.12) |
| Q4 | Should guardians be told when a child's device declares a looser age than before? | yes, in Activity (a `signal` event), not as a push |
| Q5 | The app's App Store age rating under the new questionnaire | keep the current rating; answer In-App Controls: Parental Controls yes, Age Assurance yes (57.09) |
| Q6 | Ask Apple for the Family Controls distribution entitlement now, in case it is needed later? | no (decision 6) |
| Q7 | How long to keep a child's activity | 90 days |
| Q8 | May a guardian who is not an admin lift a restriction entirely? | no; only an admin can delete a policy, a guardian can loosen it to 18+ |
