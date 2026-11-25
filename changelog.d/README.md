# Changelog fragments

Each pull request that changes what a user or a developer sees adds its changelog entry here, as
two files of its own, so two pull requests never touch the same file:

`changelog.d/2026-11-25-short-slug.md`

```markdown
### Fixed

- **What changed, in a few words.** What a user sees, in a sentence or two, each further line
  indented by two spaces.
```

`changelog.d/2026-11-25-short-slug.ar.md`

```markdown
### أُصلح

- **ما تغيّر، في كلمات قليلة.** ما يراه المستخدم، في جملة أو جملتين، وكل سطر يلي الأول مسبوق
  بمسافتين.
```

- **The name** is the day, `YYYY-MM-DD`, and a slug in lower case letters, digits and single
  hyphens. The day orders entries on the page, newest first. The branch's name is a good slug.
- **The sections**, each at most once in a file: `Added` / `أُضيف`, `Changed` / `تغيّر`,
  `Deprecated` / `أُهمل`, `Removed` / `أُزيل`, `Fixed` / `أُصلح`, `Security` / `الأمان`.
- **The two files** have the same sections, in the same order, with the same number of entries.
- **Links** are written from the top of the repository, as in `CHANGELOG.md`.

Check it: `python3 tools/dev/changelog_fragment.py check` (the lint job runs it on every pull
request). A change nobody outside the code sees needs no fragment.

## What becomes of a fragment

Nothing: it stays here. No release moves it, so no release needs a person or an edit of main.
[maktaba.dev/changelog](https://maktaba.dev/changelog/) is built from `CHANGELOG.md`,
`CHANGELOG.ar.md` and these files (`web/packages/devportal/scripts/changelog.mjs`). A fragment's
release is the first `vX.Y.Z` tag whose tree has the file, read from git at each build; one no
tag has yet is under `[Unreleased]`. Releases are tags a session makes every 30 minutes, with no
commit of their own, so folding fragments into `CHANGELOG.md` would take a commit to main for
each release, with its own pull request and CI, and would bring back the file every branch
conflicts on. The tag already says what a release contains, and the page cannot disagree with it.
A fragment corrected later stays in its release; a renamed one moves to the next release.

The `[Unreleased]` entries of `CHANGELOG.md` and `CHANGELOG.ar.md` are frozen: they were written
before the fragments, in releases since 0.2.0 that they do not name. On the page they are under
`[Unreleased]` until a release brings a fragment, then under their own heading below the
releases. To correct one, add a `Changelog-Edit: <reason>` trailer to the commit.

## A branch with lines in CHANGELOG.md

A branch begun before the fragments may still add lines there (the check warns). Bring main in
with `tools/dev/merge-main.sh` first, if the branch has no `tools/dev/changelog_fragment.py` yet:
that first run is the branch's own older script, which keeps both sides in the CHANGELOGs. Then
move the branch's entries here, in both languages:

```sh
git fetch origin main
python3 tools/dev/changelog_fragment.py convert
git commit -m "chore: the changelog entry as a fragment"
```

From then on `merge-main.sh` moves them itself when a CHANGELOG conflicts, unless one was already
resolved by hand. `convert` never changes a fragment that is there. A branch begun after the fragments that adds a line there fails the check; the
same command fixes it.

## The same entry twice

An entry (its section, its English and its Arabic) that two fragments have is on the page once, in
the earliest release that has it. Two stacked branches that each moved the same entry leave one
that way: nothing needs removing, and the check only warns. So an identical entry repeated on
purpose for a later release does not show again: say what is different this time. A branch that
adds the same entry in two fragments of its own fails the check.
