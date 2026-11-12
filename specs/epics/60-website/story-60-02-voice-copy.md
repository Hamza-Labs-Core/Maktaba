# Story 60.02 — Voice and copy: plain and warm for households, Arabic written natively

> Epic 60 · Phase 1 (part A: the voice guide) and Phase 2 (part B: the tagline and page copy) ·
> Depends on: — · Needed by: every page story

## Why

The site speaks to households, not to developers. Most of what exists about Maktaba speaks to
developers: specs, the wiki, release notes. A household needs to read what Maktaba does for it,
in words it uses at home, in its own language. An Arabic page that reads as translated English
tells an Arabic-speaking family the product was not made for it.

## Description

**Part A, the voice guide** (`web/packages/website/copy/voice.md`), short enough to read in five
minutes, in both languages:
- **Who we write for:**
  - a household with films, series and recordings, on a computer or a NAS;
  - one person sets it up, everyone watches.
- **How we sound:** plain, warm, calm, and sure without boasting. We say "you" and "your home".
  We say what something does for you before how it works.
- **Words we use, and words we don't:**

  | Use | Not |
  |---|---|
  | your films and series, your library | media assets, content |
  | your home, the computer that runs Maktaba | instance, server node, deployment |
  | watch on your TV | client playback |
  | Maktaba makes subtitles | ASR, transcription pipeline |
  | it updates itself | auto-update daemon |

  A technical word stays only where a person must type it or will see it in another product's
  screen: Docker, compose, DSM, a file name.
- **Sentences:**
  - short;
  - one idea each;
  - active voice;
  - no exclamation marks;
  - no "simply", "just" or "easy";
  - numbers before units ("4 GB").
- **Errors and problems** (for 60.06):
  - what happened, in one sentence, with no blame;
  - then what you can do;
  - then whom to ask.
- **Arabic:**
  - Modern Standard Arabic, written natively in the same voice;
  - the same register as the app's catalog (its terms: «الجهاز الرئيسي» for the home, the
    product name as the catalog writes it);
  - Arabic sentence order, not English order in Arabic words;
  - no calques ("قم بـ" + noun for every verb);
  - punctuation in Arabic forms (، ؛ ؟);
  - Western digits, as in the apps;
  - each Arabic text is written from the meaning, never translated sentence by sentence and
    never machine-translated.
- **Names:** "Maktaba" in Latin text; «مكتبة» in Arabic text, as the web catalog writes it.

**Part B, the tagline and page copy** (`copy/<page>.en.md`, `copy/<page>.ar.md`):
- **The tagline:** three proposals in each language, each written in its own language, not
  translated, with one line each on what it says. The owner chooses or asks for more (Q1).
- **Copy for the home and download pages:**
  - headlines;
  - the section texts;
  - button labels;
  - alt text for every screenshot (what the screenshot shows, not "screenshot of");
  - the meta description and the social card title of each page.
- **The Phase 1 front door's** few lines, rewritten in the voice when part B lands.
- **Review:**
  - the copy pull request lists every new text in both languages side by side;
  - the lead reviews it for the voice;
  - the owner reads and approves every Arabic page (Q2).

**What stays as written elsewhere:**
- the install guides' text (Epic 59), which is moved onto the voice only where 60.05's
  Phase 3 step touches it;
- the help topics (Epic 41);
- the problem texts, which follow part A's rule for errors and are written in 60.06.

## Acceptance criteria

- **Given** part A, **then** `copy/voice.md` exists in English and Arabic with the sections above,
  and Phase 1's pages follow it.
- **Given** part B, **then** the tagline proposals, the home and download copy and every alt text
  exist in both languages, written separately, and the owner's approval of the tagline and the
  Arabic is recorded in `copy/README.md` with its date.
- **Given** any page, **then** no English word from the "Not" column appears in its copy, and its
  Arabic passes the Latin-share check.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | lint | the built pages | scan for the "Not" list (English) and its Arabic list | none found outside code blocks |
| T02 | build | Arabic pages | the Latin-share check | under 20% |
| T03 | lint | copy files | every `.en.md` has its `.ar.md` with the same keys | pairs complete |
| T04 | unit | the built pages | every `<img>` has a non-empty `alt` in the page's language (or `alt=""` with `role=presentation` for decoration) | true |
| T05 | review | part B | owner approval | recorded in `copy/README.md` |

## Strings

Each page's copy files are its strings table. The site keeps no separate catalog.

## Edge cases

- **A word with no settled Arabic** (a NAS's menu name, "compose"): kept in Latin inside «»,
  after an Arabic description, as the install guides already do.
- **Gendered address in Arabic:** the copy uses forms that read naturally for everyone (the
  plural for "you", or impersonal forms), as the app's catalog does.
- **The tagline in a social card at 1200×630:** each proposal is checked to fit at the card's
  size in both scripts.

## Build plan

| File | Change |
|---|---|
| `web/packages/website/copy/voice.md` (en + ar) | the voice guide |
| `web/packages/website/copy/<page>.{en,ar}.md` | the page copy |
| `web/packages/website/copy/README.md` | approvals |
| `web/packages/website/scripts/lint-copy.mjs` | T01, T03 |
