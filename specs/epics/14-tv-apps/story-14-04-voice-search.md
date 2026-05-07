# Story 14.4 — Voice search integration (Siri, Google Assistant)

Voice queries dispatched to `/api/search/suggest` and `/api/search`.

**Anchors:** [`architecture.md` §6.5](../../architecture.md). Depends on
Epic 7 Story 7.8 (search), Epic 1 Story 5.4 (FTS).

## AC

- tvOS: `INSpeakableString` integration so "Hey Siri, search Maktaba for
  …" works while the app is foreground.
- Android TV: voice input from the system keyboard, plus an
  app-registered Assistant action `actions.intent.SEARCH`.
- Recognized utterances with no hits: surface "did you mean…" suggestions
  using `/api/search/suggest`.
- Locale-aware: voice in `ar` queries the Arabic FTS index; voice in
  `en` queries cross-language semantic.
- Spoken Arabic is normalized server-side (FTS5 `unicode61
  remove_diacritics 2`).

## TC

- Speak "تفسير الفاتحة" on Apple TV: results within 2 s.
- Speak an English query into Android TV's mic: results render with
  Arabic snippets (cross-language).
- Speak gibberish: empty state with "did you mean" suggestions.

## EC

- Mic permission denied: surface the OS-level permission flow.
- Background noise causes mistranscription: show the recognized text in
  the search box so the user can correct.
- Voice provider returns nothing (rare): silently fall back to text
  search.
