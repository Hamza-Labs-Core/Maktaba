# Story 58.04 — A home for store reviewers

> Epic 58 · Phase 1 · Depends on: 58.03 · Needed by: 58.05, 58.07, 58.10, 58.11, 58.13, 58.18

## Why

Every store reviews a TV app by using it:
- Amazon's Appstore review.
- Roku's certification, which checks launch time, deep links and playback start.
- Samsung's verification test.
- LG's QA function stage.
- Google's Cast console needs no review, but a published receiver must work.

Maktaba is useless without a home, and a reviewer has none. Every store asks for working test
credentials in the submission form. A reviewer who cannot sign in rejects the app, and a
reviewer who can sign in must not reach anyone's real home.

## Description

**The review home** is a Maktaba home run by the project on the cloud's infrastructure, at
`review.maktaba.tv`:
- It runs the newest released version, updated like any home (Epic 28).
- It is registered with Maktaba Cloud like a customer's home, so the cloud code (58.03) and the
  relay are exercised exactly as a customer's are.
- Its content is public-domain and openly licensed only: a few short films, a lecture series, a
  public-domain audiobook and one live channel looped from those. Each item has an Arabic and an
  English title.
- Its test files cover the paths a reviewer will hit: 4K HEVC HDR10, 1080p H.264, an MKV with
  EAC3 and WebVTT subtitles in English and Arabic, and one file that needs a transcode.

**The reviewer's account** is a viewer, never an admin, with all libraries. It has no parental
policy, so it shows everything.

**How the reviewer gets in** is written in each store's submission notes (story 58.16 keeps a
template per store under `docs/stores/`):
- **Fastest path:** on the TV's first screen choose **Sign in with a code**. The reviewer is given
  a web page, `review.maktaba.tv/approve`, signed in with the reviewer account's password, where
  they type the TV's code. That is the same cloud code flow a customer uses, with the review
  home as the home.
- **Fallback:** type `review.maktaba.tv` as the home's address and sign in with the username and
  password.

The password lives in the release secrets store (the same place as the store upload keys,
`docs/google-play.md`'s pattern). It is never written in the repo, the trail or a submission
template. The template names the secret, and the release session fills it in.

**Abuse:** the review home's account can only watch. Its streams go through the relay at the
lowest plan cap. Its password rotates after each submission round (a release checklist step),
and a stream from it counts in the cloud's analytics like any other.

## Acceptance criteria

- **Given** a fresh TV app build, **when** a reviewer follows the submission notes, **then** they
  are watching a video within 2 minutes, on each platform.
- **Given** the reviewer account, **when** it calls any admin route, **then** 403 (it is a
  viewer; covered by the role tests of 58.17).
- **Given** the repo, **then** no review credential appears in it (`tools/ci` secret scan, and the
  templates hold only the secret's name).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Scheduled check (the cloud's monitoring, Epic 50 story 50.10) | review home | every 15 min: discovery, a cloud code approved by a test client, one stream session per format | alert on failure |
| T02 | Docs lint | `docs/stores/*.md` | scan | no password-shaped value; names the secret |
| T03 | Content check | the review home's library | list | every item carries a licence note (public domain or CC) and both titles |

## Strings

None in the apps: the review home is a normal home.

## Edge cases

- **The review home is mid-update during a review**: updates to it are held while a submission
  is open (the release session sets `skipped_versions` for the review window).
- **A store reviewer in a region the relay blocks**: the direct HTTPS name of story 50.15 is the
  second way, and both are listed.

## Build plan

| File | Change |
|---|---|
| `deploy/review-home/` (new) | how the review home is run and updated, beside the cloud's deploy |
| `docs/stores/{amazon,roku,samsung,lg,cast}.md` (new) | submission note templates, naming the secret |
| `cloud/` monitoring config | T01 probe |
