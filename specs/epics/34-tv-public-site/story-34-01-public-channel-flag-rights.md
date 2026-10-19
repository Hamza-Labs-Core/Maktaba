# Story 34.1 — Public channel flag and rights

## Description

*As the owner, I want to choose which channels the public site shows, confirm I hold the rights for
them, and keep particular series or items off the public feed, so that nothing I may not publish
goes out.*

Design: [design.md](design.md) §2; README R1.

## Acceptance criteria

- **AC1** `tv_public_channels.public` defaults to false. `PUT /api/tv/public/channels/{id}
  {public:true}` without `rights_confirmed:true` returns 422 `rights_confirmation_required`; with it,
  the row stores `rights_confirmed_by`, `rights_confirmed_at`, `rights_note`.
- **AC2** Turning a channel private keeps the audit fields; turning it public again requires a new
  confirmation.
- **AC3** Exclusions: add/remove `{kind: series|collection|item|tag, ref, mode: exclude|clear,
  reason}`; `tvpublic.Availability(block)` follows README R1.3 for the `allow` and `deny` policies,
  and design §2.1 for filler and live blocks.
- **AC4** The admin "Public site" page lists channels (public, rights, slug, on air, host, viewers,
  pin always-on), the exclusions editor with the next 7 days of affected slots, and "Preview public
  feed"; English and Arabic (RTL).
- **AC5** Series that entered a public channel's schedule since its last rights review are listed
  as "new on a public channel" with "Mark reviewed" / "Exclude" (non-blocking).
- **AC6** `slug` is unique, `[a-z0-9-]{2,40}`, defaults to the channel slug; `public_id` is the
  HMAC of the channel id with `site_salt` and never changes for a channel.
- **AC7** Panic off-air (`POST /api/tv/public/off-air {on}`) is stored, sent to the site (34.4) and
  shown as a red banner on the admin page until cleared.
- **AC8** Making a channel public when encoder capacity minus `reserve_slots` would go negative
  shows a warning with the numbers and asks for confirmation (design §5.4).

## Test cases

- **TC1** `TestPublicRequiresRights`, `TestPrivateKeepsAudit`.
- **TC2** `TestAvailabilityAllowPolicy`, `TestAvailabilityDenyPolicy`, `TestAvailabilityFillerAndLive`.
- **TC3** `TestSlugValidationAndUniqueness`, `TestPublicIDStable`.
- **TC4** `TestNewSeriesOnPublicChannel`.
- **TC5** web: `PublicSitePage.test.tsx` (rights dialog, exclusions editor, RTL snapshot).

## Edge cases

- **EC1** A collection exclusion whose collection changes later: membership is evaluated at
  publish/slate time, not stored.
- **EC2** A channel deleted while public: its row turns private, its inputs are released (34.3).
- **EC3** Both an exclude and a clear match: exclude wins.

## Dependencies

Epic 27 channels and blocks; Epic 26 collections; the Epic 34 migration (next free slot at implementation time).
