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

A fragment stays here after release. [maktaba.dev/changelog](https://maktaba.dev/changelog/)
is built from both legacy CHANGELOGs and the fragments. The portal uses the earliest dated
`vX.Y.Z` tag containing the fragment, with the version as a tie-breaker. For annotated tags it
uses the tag date, and for lightweight tags it uses the tagged commit date. A fragment absent
from all release tags is under `[Unreleased]`.

Correct a fragment in place to keep it in its original release. Removing or renaming a
committed fragment requires a `Changelog-Edit: <reason>` trailer. Renaming also changes the
path the portal uses to find its first release.

The legacy release sections, including `[Unreleased]`, are frozen. A recognizable correction
in its original section requires a `Changelog-Edit: <reason>` trailer. Add new entries as
fragments. The portal shows legacy `[Unreleased]` entries under their own heading once a release
includes a fragment.

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

After that, `merge-main.sh` moves entries when a CHANGELOG conflicts. If a CHANGELOG was
resolved by hand, the script keeps that resolution. When an entry moves between sections or
looks like an unrelated replacement, conversion stops for manual placement. Existing
fragments stay as they are.

## The same entry twice

Matching wording in either language within a section identifies the same entry. The portal
shows the bilingual pair once, in the earliest release that has it, or from the older filename
if both are unreleased. Correct the original pair. When both translations need a rewrite,
remove any stale copy with a `Changelog-Edit: <reason>` trailer so readers see the correction.

The gate rejects repeats within a fragment or across fragments the branch adds. A duplicate
already on the base or brought in by a merge produces a warning. A child rebased onto a
converted parent may have both fragments counted as its own when checked against main. Compare
against the parent when it is the PR target, or remove the duplicate before targeting main.
