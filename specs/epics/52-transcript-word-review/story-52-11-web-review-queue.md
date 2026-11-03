# Story 52.11 — Web review queue

> Epic 52 · Phase 2 · Depends on: 52.8, 52.9, 52.14 · Needed by: 52.13 (its wording)

## Why

The review queue is where a person spends their time. Reviewing words is repetitive, so every
second per word matters. The page shows one word at a time, plays its audio at once, and resolves
with one key. Tools like Descript, Rev and Subtitle Edit all work this way (research §4). It must
work as well in Arabic as in English, right to left, with every string in both languages.

## Description

### 1. Where it lives

- A **Word review** entry in the main navigation. It shows **only for home admins** (README §8,
  decision 1): users with `write` or the library `admin` role on a library never see it, nor the
  transcript tab's edit mode (52.12). Hiding it is a courtesy; the API's 403 is the rule (52.8).
  It carries a badge with the open count.
- Route `/review`, page `web/src/pages/review/Review.tsx`. The list and filter patterns come from
  `web/src/pages/queue/Failures.tsx`.
- Sub-routes: `/review/history` (edits, with undo) and `/review/vocabulary` (52.10's page).

### 2. Layout

- **Left (right in RTL): the list.** Open flags by priority, one line each: the word, its reason
  icons with text labels, the video title, and the language. Filters: library, language, reason,
  status. "Show locked" is off by default.
- **Main: the word.**
  - The sentence, with the word highlighted (a box and underline, not only a colour), and the
    sentence before and after in a lighter tone.
  - The clip player: play/pause, 0.75× and 1×, replay. The clip auto-plays when the word opens.
  - The reasons in words ("Whisper was unsure", "The second listen heard «…»", "Not in the Arabic
    dictionary", "Looks like a name", "Differs from the Quran text").
  - For a `quran_mismatch` (52.15): the verse reference («البقرة ١٨٣» / "Al-Baqara 2:183"), the
    verse's word ("The verse has: …" / «في الآية: …»), and the credit line "Quran text: Tanzil
    Project (tanzil.net)" / «نص القرآن: مشروع تنزيل (tanzil.net)». "Add to dictionary" is hidden
    for these, since a verse word is checked against the verse, not the dictionary.
  - Candidates 1-5 as buttons, with their source.
  - An edit field with the word.
  - Actions: Accept, Correct, Name, Add to dictionary, Skip.
  - "This word is flagged in N other places", with a checkbox for "Apply to all", a preview of 5
    sentences, and the video link at the word's time.
- **Footer:** a toast after each action, "Fixed «…» → «…» · Undo".

### 3. Keyboard

| Key | Action |
|---|---|
| `J` / `↓` | Next word |
| `K` / `↑` | Previous word |
| `Space` | Play / pause the clip |
| `Shift+Space` | Play at 0.75× |
| `R` | Replay from the start |
| `Enter` | Accept as is (or save the edit when the field has focus) |
| `1`-`5` | Choose that candidate (corrects at once) |
| `E` | Edit: focus the field with the word selected |
| `N` | Mark as a name |
| `D` | Add to dictionary |
| `S` | Skip |
| `A` | Toggle "Apply to all N places" |
| `U`, `Ctrl+Z` / `Cmd+Z` | Undo the last action |
| `?` | Show the shortcuts sheet |
| `Esc` | Leave the edit field |

Keys are physical (`KeyboardEvent.code`), so they work with an Arabic keyboard layout. Letter
shortcuts are off while the edit field has focus. The next word opens by itself after an action.

### 4. Right to left and scripts

- The UI chrome follows the UI language (`ar` → `dir="rtl"`), as in the rest of the web.
- The sentence, the word, the candidates and the edit field take the *transcript's* direction
  from its pack (`direction`), with `dir="auto"` on each candidate. An English sentence in the
  Arabic UI reads left to right, and the reverse.
- The fonts are Amiri for Arabic text and Plex for the rest, as in the theme (trail §2, "Look and
  feel"). Harakat are shown when present.
- In RTL, `J`/`K` and the arrows keep their meaning (next/previous), not their screen direction.

### 5. Strings (English + Arabic in the same PR)

The keys live under `review.*` in `web/src/i18n/en.json` and `ar.json`: `review.title`,
`review.empty`, `review.next`, `review.prev`, `review.play`, `review.slow`, `review.accept`,
`review.correct`, `review.name`, `review.dictionary`, `review.skip`, `review.undo`,
`review.applyAll` (with a plural count), `review.otherPlaces` (plural), `review.lockedBy`,
`review.stale`, `review.reason.low_confidence`, `review.reason.not_in_dictionary`,
`review.reason.listeners_disagree`, `review.reason.quran_mismatch`, `review.reason.named_entity`,
`review.reason.near_vocabulary`, `review.reason.previous_fix`, `review.reason.manual`,
`review.quran.*` (52.15: expected, missing, inserted, credit),
`review.source.*`, `review.shortcuts.*`, `review.fixed`, `review.history.*`, `review.filters.*`.
The Arabic plurals use the six ICU categories. The help page is
`web/src/help/content/{en,ar}/word-review.md` (Epic 41), linked from the page.

### 6. Accessibility

- The focus order is list, sentence, player, candidates, field, actions.
- Each reason has an icon *and* text. The highlighted word has `aria-current` and a screen-reader
  label ("Suspect word: …").
- Live region announcements after each action. Every control can be reached and used without a
  mouse.
- Contrast passes WCAG AA in both themes.

## Acceptance criteria

1. **Given** a home admin with open flags, **when** they open `/review`, **then** the top-priority
   word is shown with its sentence, and its clip starts playing within 700 ms on the LAN.
2. **Given** the word is open, **when** they press `2`, **then** it is corrected to the second
   candidate, a toast with Undo shows, and the next word opens.
3. **Given** a correction just made, **when** they press `U`, **then** the edit is undone (52.9),
   the word is open again, and the toast says so.
4. **Given** the Arabic UI and an Arabic transcript, **then** the page is right to left, the
   sentence reads right to left, and `J` still moves to the next word.
5. **Given** the English UI and an Arabic transcript (or the reverse), **then** the chrome follows
   the UI language and the sentence follows the transcript's direction.
6. **Given** an Arabic keyboard layout, **when** the reviewer presses the physical `N` key,
   **then** the word is marked as a name.
7. **Given** the edit field has focus, **when** they type «ن», **then** the letter is typed and no
   shortcut fires. **When** they press `Enter`, **then** the correction is saved.
8. **Given** a word flagged in 37 other places, **when** they press `A` and then `Enter` on a
   candidate, **then** all 38 change as one batch, and one Undo reverses all of them.
9. **Given** a word another admin has open, **then** it shows "Being reviewed by <name>" /
   «يراجعها <name>» and its actions are off until the admin chooses Take over / «تولَّ المراجعة».
10. **Given** a user with only `read`, **or** a user with `write` or the library `admin` role who
    is not a home admin, **then** the navigation shows no Word review entry, and `/review` shows
    "Only home admins can review words" / «مراجعة الكلمات للمشرفين فقط».
13. **Given** a `quran_mismatch` flag, **then** the verse reference, the verse's word and the
    Tanzil credit show in the current UI language, and "Add to dictionary" is hidden.
11. **Given** a CI run, **then** the missing-Arabic test fails if any `review.*` key lacks an
    Arabic string.
12. **Given** no open flags, **then** the page says "No words to review" with the time of the
    last check.

## Build plan

### Files

- `web/src/pages/review/Review.tsx`, `ReviewList.tsx`, `WordPanel.tsx`, `ClipPlayer.tsx`,
  `Shortcuts.tsx`, `History.tsx`.
- `web/src/lib/reviewApi.ts` (52.8), `web/src/lib/useReviewQueue.ts` (prefetches the next 3
  flags and their clip URLs, so the next word opens at once).
- `web/src/styles/review.css`.
- `web/src/App.tsx` (route), the navigation component (entry and badge).
- `web/src/i18n/en.json`, `web/src/i18n/ar.json`.
- `web/src/help/content/en/word-review.md`, `web/src/help/content/ar/word-review.md`.

### Tests

- Vitest + Testing Library: each shortcut does its action; shortcuts are off in the field;
  `KeyboardEvent.code` with an Arabic layout; RTL direction for chrome vs sentence; plural
  strings; the empty, locked and stale states.
- `reviewApi` mocked with MSW: prefetch, toast with undo, error states (403, 409 locked, 409
  stale).
- Playwright e2e on the branch stack (scratch Postgres seeded with a transcript, `word_data` and
  flags): open the page, hear a clip (audio element `currentTime` advances), correct with `1`,
  undo with `U`, bulk-apply 3 flags, and in the Arabic UI, all in the dark theme with screenshots
  kept in the CI report.
- axe check on the page in both themes.

### Rollout

Phase 2 release, together with 52.8, 52.9 and 52.14. Only home admins ever see it (README §8,
decision 1).

## Edge cases

- **The clip fails** (file offline): the player shows "Audio not available", and the actions still
  work.
- **A flag goes stale while open** (someone edited the sentence): the panel reloads it with a
  notice.
- **Very long sentences:** the context is cut to 25 words either side of the word, and the full
  sentence is shown on expand.
- **Mixed-script sentence:** each script run is wrapped in its own `<bdi>`, so punctuation does not
  jump.
- **A slow phone browser:** the layout stacks at narrow widths, and the actions stay one tap.
