# Story 17.5 — Error states and empty states

Every screen has a documented error and empty state with copy that
explains and a primary recovery action.

**Anchors:** [`architecture.md` §6](../../architecture.md).

## AC

- Error states classified: `network`, `server`, `permission`,
  `not_found`, `validation`. Each has a token-driven illustration and
  copy template.
- Empty states classified: `first_run`, `filtered_out`, `cleared`. Each
  has a primary CTA.
- Copy follows a tone guideline: clear, direct, no blame.
- Error toasts: 4 s default; sticky for `permission` and `not_found`.
- Retry actions are idempotent and de-duped (and use the
  `Idempotency-Key` contract from
  [Story 11.10](../11-web-ui/story-11-10-offline-pwa.md)).

## TC

- Disconnect network and load the library: a network error illustration
  + "Try again" button appears.
- Filter library to nothing: empty illustration + "Clear filters".
- A 404 on a deep-linked video: "Video not found" + "Return to library".

## EC

- An error during an error (retry fails the same way): single
  consolidated message; no error storm.
- A permission error caused by a missing library scope: surface "Ask
  your admin to grant access".
- A user dismisses an error then re-triggers it within 5 s: show only
  once, dedupe.
