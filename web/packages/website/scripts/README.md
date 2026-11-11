# The maktaba.tv link check (story 60.08)

`links.mjs` checks that every maktaba.tv link the repository prints opens a page. Node's
standard library only, so it runs with no install:

```sh
node web/packages/website/scripts/links.mjs            # the check (CI: lint / maktaba.tv links)
node --test web/packages/website/scripts/links.test.mjs  # its tests
node web/packages/website/scripts/links.mjs --paths    # the paths it leaves out
node web/packages/website/scripts/links.mjs --dist web/packages/website/dist    # against a build
node web/packages/website/scripts/links.mjs --write-manifest web/packages/website/dist
node web/packages/website/scripts/links.mjs --prune-baseline
```

- **Problem links** (`/problems/<slug>`, `/ar/problems/<slug>`) are checked against the
  registry, `shared/api/problems.json` (story 60.06). With no registry, no slug is registered.
- **Every other link** is checked against `../dist-manifest.json`: every file of the built
  site and the `id`s on each page, plus the Worker's redirects (which are not files). The
  site build regenerates it with `--write-manifest <dist>`, which keeps `redirects`.
- **`../links-allow.json`:** links that are not links (the two deep-link parser comments),
  each with its reason. A new entry is reviewed like code; an entry whose link has left its
  file fails.
- **`../links-baseline.json`** is temporary. When the check landed maktaba.tv served nothing,
  so every link was dead; each one is listed with the story whose page makes it live (60.03,
  60.05, 60.06). A dead link not in the baseline fails, and so does an entry whose link now
  opens a page or has left its file, so the baseline only shrinks: the pull request that ships
  a page runs `--prune-baseline` (it only removes entries). **The baseline must be empty by the
  end of Epic 60's Phase 1**; the check then drops it.
- The release notes template (`tools/release/releasenotes/notes.md.tmpl`) is read as text like
  any file; its Go test (`links_test.go`) renders it with every optional line on and requires
  each maktaba.tv link to be a literal of the template, so this check sees every one.
