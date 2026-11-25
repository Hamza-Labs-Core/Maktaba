# Pull requests

Every change reaches `main` through a pull request on
[GitHub](https://github.com/Hamza-Labs-Core/Maktaba/pulls) that passes `ci-success`.

## 1. Branch

Start from an up-to-date `main` and create a branch named for the change:

```sh
git fetch origin
git switch -c fix/waiting-banner-state origin/main
```

Prefixes such as `feat/`, `fix/`, `docs/`, `test/` and `chore/` match the commit types in
[Conventions](conventions.md). Contributors without write access work from a fork.

Keep one concern per pull request. A large effort can land as one pull request built up
from several commits, rather than many small dependent ones.

## 2. Make the change, with its tests

- Update the tests in the same change ([Running the tests](tests.md)).
- Ship every user-facing string in English and Arabic.
- Enforce any role rule in the API, with a role test.
- If the change alters behaviour users see, update the user guide in `web/src/help/`.

## 3. Check locally

```sh
make lint
make test-unit
make test-integration   # if you touched integration boundaries (database, gRPC, queue)
make test-e2e           # if you touched user-visible flows
```

With the pre-push hook installed, `git push` also runs the affected-tests gate and the
affected tests that are safe to run locally.

## 4. Open the pull request

```sh
git push -u origin fix/waiting-banner-state
gh pr create --base main
```

The body follows
[.github/PULL_REQUEST_TEMPLATE.md](https://github.com/Hamza-Labs-Core/Maktaba/blob/main/.github/PULL_REQUEST_TEMPLATE.md):

```markdown
## Summary

- What changed and why, in 1 to 3 bullets. Link the story or issue.

## Test plan

- [ ] `make lint`
- [ ] `make test-unit`
- [ ] `make test-integration` (if changes touch integration boundaries)
- [ ] `make test-e2e` (if changes touch user-visible flows)

## Force-merge override (delete if N/A)

force-merge: <one-sentence reason; at least 10 characters>
```

- Fill in the test plan with what you actually ran, plus any manual checks (for
  example, the screen checked in Arabic, or the app tried on a simulator).
- Delete the force-merge section unless the `force-merge` label is on the PR; the
  `pr-body-check` job reads that line as the audit trail.
- Add screenshots for UI changes, in both languages where layout differs.

## 5. CI

CI runs the jobs your changed files select ([CI](ci.md)). Jobs for unrelated code do not
run and do not block the merge. Fix any failure in the same branch; a red job caused by
the change is never merged around.

## 6. Review

- Every pull request gets a code review before merge. Reviewers look for correctness,
  tests that cover the change, the conventions above, and security
  ([Security rules](../security/conformance.md)).
- Answer each comment with a fix or a reason. Push fixes as new commits so the review
  history stays readable.
- Re-run the related checks after the fixes.

## 7. Merge

When `ci-success` is green and review is done, merge into `main`. Delete the branch
afterwards. Releases are cut by tagging green commits on `main`.

## Docs and spec changes

Changes limited to `specs/`, `docs/` or Markdown files run only the jobs whose filters
include those files, so they usually finish quickly. They still need a clear summary and
a review.

## English and Arabic

maktaba.dev carries every page in English and Arabic, and its build fails when the two
drift, so a change to what it publishes lands in both languages in the same pull request:

- **A guide** in `docs/developers/` has its Arabic twin beside it (`name.md` and
  `name.ar.md`). Change both, or add both for a new guide.
- **A changelog entry** is two new files in `changelog.d/`, `YYYY-MM-DD-slug.md` and, in
  Arabic, `YYYY-MM-DD-slug.ar.md`: the same sections with the same entries
  ([the changelog](#the-changelog)).
- **The API description:** a new or changed summary or description in
  `shared/api/openapi.json` gets its Arabic in `shared/api/openapi.ar.json` (keyed by its
  JSON pointer, with the English it translates).

Write natural Modern Standard Arabic. Code, commands, file names and identifiers stay as
written.

## The changelog

A change a user or a developer sees gets a changelog entry, in two new files of its own, so two
pull requests never conflict on it. The name is the day and a short slug:
`changelog.d/2026-11-25-short-slug.md` holds

```markdown
### Fixed

- **What changed, in a few words.** What a user sees, in a sentence or two, each further
  line indented by two spaces.
```

The Arabic file, `changelog.d/2026-11-25-short-slug.ar.md`, has the same sections, named in
Arabic, with the same number of entries. The files hold sections and entries only. The sections, English
and Arabic:

| English      | Arabic   |
| ------------ | -------- |
| `Added`      | `أُضيف`  |
| `Changed`    | `تغيّر`  |
| `Deprecated` | `أُهمل`  |
| `Removed`    | `أُزيل`  |
| `Fixed`      | `أُصلح`  |
| `Security`   | `الأمان` |

`python3 tools/dev/changelog_fragment.py check` checks the pair, as the lint job does on every
pull request; it also refuses a new line under `[Unreleased]` in `CHANGELOG.md`, whose entries
are from before these files. A pull request that began before them may still have lines there;
`python3 tools/dev/changelog_fragment.py convert` moves them to a pair, and
`tools/dev/merge-main.sh` does it when they conflict with `main`. A branch that does not have that
tool yet brings `main` in first, with its own `merge-main.sh`, which keeps the lines where they are.

The [changelog](https://maktaba.dev/changelog/) page puts each entry in the release that brought
it: the first version tag that contains its file. Nothing is edited when a release is made.

## Security fixes

Do not open a public pull request or issue that describes an unfixed vulnerability.
Report it to security@maktaba.dev first; see the
[reporting policy](https://maktaba.dev/security/policy/).
