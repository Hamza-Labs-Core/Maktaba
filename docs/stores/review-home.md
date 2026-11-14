# The store reviewers' home: review.maktaba.tv

Every TV store reviews an app by using it (Epic 58 story 58.04): Amazon's Appstore review,
Roku's certification, Samsung's verification test, LG's QA stage; Google's Cast console has no
review, but a published receiver must work. Maktaba does nothing without a home, and a reviewer
has none. So the project runs one: **review.maktaba.tv**, a Maktaba home like any household's,
with public-domain and openly licensed content only, registered with Maktaba Cloud so the
cloud code (story 58.03) and the relay are exercised exactly as a customer's are.

How it is run, updated and provisioned: [`deploy/review-home/`](../../deploy/review-home/README.md).

## What a reviewer gets

- **A viewer account**, `reviewer`, never an admin, with every library and no parental limit:
  it shows everything. Its password is the release secret `REVIEW_HOME_VIEWER_PASSWORD`.
- **A restricted child account**, `reviewer-child`, limited to 9+ with unrated items hidden,
  for the parental checks (Epic 57): signed in as the child, the older viewers' library is out
  of reach and Ask a parent goes to `reviewer`, who looks after it. Its password is the release
  secret `REVIEW_HOME_CHILD_PASSWORD`.
- **The content**, each item with an English and an Arabic title and its licence note
  ([`content/manifest.json`](../../deploy/review-home/content/manifest.json), and LICENCES.md
  beside the files on the home):

  | Library | Items | Licence |
  |---|---|---|
  | Films · أفلام (all ages) | Spring, Meridian, Big Buck Bunny | CC BY 4.0, CC BY 4.0, CC BY 3.0 |
  | Films for older viewers · أفلام للكبار (13+) | Sintel, Cosmos Laundromat: First Cycle | CC BY 3.0, CC BY 4.0 |
  | Lectures · محاضرات | Math for Teens 1–3 (logic, sets, induction) | CC BY 3.0 |
  | Audiobooks · كتب مسموعة | Stories from the Arabian Nights, parts 1 and 2 (LibriVox) | public domain |

  and **Review TV · قناة المراجعة**, a live channel looped from the films.
- **The test files** a reviewer's TV will hit:

  | Path | File |
  |---|---|
  | 4K HEVC HDR10 | Meridian (HEVC Main 10, PQ, BT.2020, HDR10 metadata) |
  | 1080p H.264 | Spring |
  | MKV with EAC3 and WebVTT subtitles in English and Arabic | Cosmos Laundromat: First Cycle |
  | a file that needs a transcode | Big Buck Bunny (Theora and Vorbis, which no TV plays) |

## How the reviewer gets in

1. **Fastest: Sign in with a code.** On the TV's first screen choose **Sign in with a code**. The
   TV shows a code. On a computer or phone, open **https://review.maktaba.tv/approve**, sign in
   as `reviewer`, type the code and confirm. The TV signs in within a few seconds. This is the
   cloud code flow a customer uses (story 58.03), with the review home as the home.
2. **Fallback: the home's address.** Type `review.maktaba.tv` as the home's address and sign in
   with the username and password.

The TV reaches the home by the best connection the cloud lists: the home's direct HTTPS name
(story 50.15) or the relay. A reviewer in a region that blocks the relay still has the direct
name, and the fallback above is that name, so both ways are always listed.

## The submission notes

Each store's page ([amazon](amazon.md), [samsung](samsung.md), [lg](lg.md), [roku](roku.md),
[cast](cast.md)) points here. The text below goes in the store's notes for the reviewer (the
testing instructions, the credentials fields). It holds only the secrets' names, in double
braces: **the release session fills in each password from the release secrets when it
submits**, and nothing with a password in it is ever committed, pasted into the trail or kept
in a template (`tools/ci/review_credentials.py` checks this on every pull request).

```text
Maktaba plays a household's own media from their Maktaba home. For review, sign in to our
review home, which carries public-domain and openly licensed films, lectures and an audiobook.

Username: reviewer
Password: {{REVIEW_HOME_VIEWER_PASSWORD}}

Fastest way in:
1. On the app's first screen, choose "Sign in with a code". The TV shows a code.
2. On a computer or phone, open https://review.maktaba.tv/approve and sign in with the
   username and password above.
3. Type the code from the TV and confirm. The TV signs in within a few seconds.

Other way in: choose to type the home's address, enter review.maktaba.tv, then sign in with
the username and password above.

To try parental controls, sign in as "reviewer-child" (password:
{{REVIEW_HOME_CHILD_PASSWORD}}): the films for older viewers are hidden and "Ask a parent"
sends a request to "reviewer".

Test files: Meridian (4K HDR10), Spring (1080p), Cosmos Laundromat (MKV with Dolby Digital Plus
and English and Arabic subtitles), Big Buck Bunny (converted by the home while it plays). The
app is in English and Arabic: switch the TV's language to Arabic to see it right to left.
Nothing is sold in the app.
```

A store's own fields (Amazon's "Testing instructions", Roku's "Channel credentials", Samsung's
"Test account", LG's QA notes) take the same text; each store page says where.

## A submission round

**Before submitting**, the release session holds the review home's updates for the review
window, so a reviewer never meets it mid-update: Admin → Updates → **Defer** until the round is
expected to close, or skip each version released meanwhile (Admin → Updates → **Skip**,
`skipped_versions`). It checks that the scheduled check (below) is green.

**After each submission round** (the store accepted or rejected the build), the release
checklist's step:

1. Make two new passwords (at least 16 characters each, different from each other) and put
   them in the release secrets as `REVIEW_HOME_VIEWER_PASSWORD` and
   `REVIEW_HOME_CHILD_PASSWORD`, replacing the old ones (also the repository secret the
   scheduled check reads).
2. On the review home's machine, with the new values in the environment of that one command:
   `deploy/review-home/seed.sh rotate`. Both accounts get the new passwords and every device
   and browser they were signed in on is signed out, the reviewers' TVs included.
3. Lift the update hold (Admin → Updates: clear the deferral or the skips).

A round whose step is not done leaves a password some store's reviewers have read working for
the next round's reviewers too; rotating is what bounds it.

## What keeps it from being abused

- **It can only watch.** `reviewer` is a viewer: every admin route answers 403 (the role tests
  of story 58.17), and the seeding tool refuses to touch an account of that name that is an
  admin. A sign-in through a cloud code is never admin either (story 58.03).
- **The lowest plan's cap.** The review home's cloud account is on the lowest plan that
  includes the relay (`basic` today: relayed streams capped at 3 Mbps, two at a time), so its
  relayed streams get that plan's cap (`cloud/internal/billing` `LowestRelayPlan`; a test fails
  when the deploy's `REVIEW_HOME_PLAN` differs).
- **The password rotates** after each submission round (above).
- **Its streams count** in the cloud's analytics like any other home's.

## The scheduled check (T01, T03)

Every 15 minutes `.github/workflows/review-home-probe.yml` (on when the repository variable
`REVIEW_HOME_PROBE` is `true`) runs [`tools/review_home/probe.py`](../../tools/review_home/probe.py)
from outside the cloud: discovery, the reviewer's sign-in, the content check (every item with
both titles and a licence note, every manifest item listed), one stream session per test file,
and a cloud code made at Maktaba Cloud, approved on the home as the reviewer and redeemed by
the test client on the first connection that answers, which then signs itself out. A failed
check fails the run (GitHub mails the owner), and with the witness's Pushgateway configured,
`review_home_down` alerts ([runbook](../runbooks/cloud/review-home-down.md)).
