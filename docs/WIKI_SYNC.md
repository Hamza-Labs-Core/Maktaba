# Wiki sync — why GitHub Wiki is not the source of truth

## TL;DR

The Maktaba wiki lives at [`docs/wiki/`](wiki/) (markdown) and is served
interactively by [`web/wiki-app/`](../web/wiki-app/) (React + Vite, reads
the JSON databases in [`docs/wiki/db/`](wiki/db/)). The GitHub Wiki tab
on [github.com/Hamza-Labs-Core/Maktaba/wiki](https://github.com/Hamza-Labs-Core/Maktaba/wiki)
is **not synced** and should not be used.

If the GitHub Wiki appears empty or only has a "Home" page that points
back to this repo, that is intentional.

## Why not GitHub Wiki?

The wiki is a derived artifact — it is regenerated from JSON, story
specs, OpenAPI, mockup filenames, and diagram filenames. GitHub Wiki
has three properties that make it a bad fit for that pipeline:

1. **It lives in a separate `.wiki.git` repo.** Pushing the wiki on
   every code change would mean a second commit, a second history to
   audit, and a second place reviews can drift out of sync with `main`.
2. **It can't render the interactive bits.** The wiki app surfaces a
   typed cross-reference graph (Linear ↔ story ↔ plan ↔ endpoint ↔
   migration ↔ diagram), per-epic dashboards, mockup previews, and
   diagram previews. None of that fits in a static markdown page.
3. **Search is page-local.** GitHub Wiki's search does not query across
   the JSON shards behind our pages, so it would miss most cross-refs.

Pinning the wiki to the same commit as the code (`docs/wiki/` is in
`main`) means every PR that changes a story, endpoint, or migration
ships its wiki update in the same review. No drift, one history.

## Where to read the wiki

| Audience | How |
|---|---|
| **Browsing markdown directly** | Open [`docs/wiki/INDEX.md`](wiki/INDEX.md) on GitHub or in your editor. |
| **Interactive (search, cross-refs, diagrams, mockups)** | `cd web/wiki-app && ./serve.sh` — Vite starts on http://localhost:5173. |
| **Machine consumption** | Read [`docs/wiki/db/wiki.json`](wiki/db/wiki.json) directly. Schema at [`docs/wiki/db/wiki-schema.json`](wiki/db/wiki-schema.json). |

## What lives where

```
docs/wiki/                            # Human-readable derived markdown
├── INDEX.md                          # Top-level index — start here
├── linear-map.md, stories-map.md     # Cross-reference catalogs
├── epics/epic-NN-*.md                # Per-epic pages (25 of them)
└── db/
    ├── wiki.json                     # Unified, canonical source
    ├── wiki-schema.json              # JSON schema
    ├── wiki-cross-refs.json          # Linear ↔ file cross-refs
    └── generate_wiki.py              # Regenerates the markdown shards

web/wiki-app/                         # React + Vite wiki reader
├── src/                              # App code
├── public/                           # Static assets
└── serve.sh                          # ./serve.sh → dev; ./serve.sh build → preview
```

## If you landed on the GitHub Wiki

A `Home.md` page may exist on the GitHub Wiki at
[github.com/Hamza-Labs-Core/Maktaba/wiki](https://github.com/Hamza-Labs-Core/Maktaba/wiki)
that redirects you here. Its sole content is a link back to
[`docs/wiki/INDEX.md`](wiki/INDEX.md) and to [`web/wiki-app/`](../web/wiki-app/).

If a maintainer wants to suppress the GitHub Wiki tab entirely:

```sh
gh api -X PATCH repos/Hamza-Labs-Core/Maktaba -f has_wiki=false
```

We have left it enabled so the redirect page can exist for anyone who
follows an old link.

## Regenerating the wiki

The derived markdown under `docs/wiki/` is produced from
`docs/wiki/db/wiki.json`. Edit the JSON and re-run the generator:

```sh
cd docs/wiki/db
python3 generate_wiki.py
```

Then commit the regenerated `.md` files alongside the JSON change. CI
verifies the markdown matches the JSON, so don't hand-edit the derived
pages.

## Seeding the GitHub Wiki redirect page (one-time, for maintainers)

GitHub will not create the `.wiki.git` repo until at least one page is
saved through the web UI. To seed the redirect:

1. Visit https://github.com/Hamza-Labs-Core/Maktaba/wiki and click
   **Create the first page**.
2. Title it `Home` and paste the contents of
   [`docs/github-wiki-home.md`](github-wiki-home.md).
3. Save. Subsequent edits can be pushed by cloning
   `git@github.com:Hamza-Labs-Core/Maktaba.wiki.git` and committing
   normally.
