# maktaba.tv's fonts

**Maktaba Text** is IBM Plex Sans Arabic (version 1.1, the copy `apps/apple` ships), subset
for the site. It sets both scripts.

| File                            | Weight | Characters                                                  | Size  |
| ------------------------------- | ------ | ----------------------------------------------------------- | ----- |
| `maktaba-text-400-latin.woff2`  | 400    | Basic Latin, Latin-1, general punctuation, arrows           | 14 KB |
| `maktaba-text-600-latin.woff2`  | 600    | the same                                                    | 15 KB |
| `maktaba-text-400-arabic.woff2` | 400    | Arabic, its presentation forms, the bidi marks and isolates | 26 KB |
| `maktaba-text-600-arabic.woff2` | 600    | the same                                                    | 28 KB |

- **Per page:** an English page downloads the two Latin files (29 KB); an Arabic page all four
  (83 KB), because its numbers, names and code are Latin. Epic 60's budget is 150 KB per page
  per language, which leaves room for part B's display face.
- **Hinting is dropped** (`--no-hinting`, a quarter of the size). Current browsers on every
  system render the outlines without it.
- **Licence:** [`OFL.txt`](OFL.txt), the SIL Open Font License 1.1. The licence reserves the name
  "Plex" for the original, and a subset is a Modified Version, so the files are renamed
  "Maktaba Text" (name table and CSS family). Their description field says what they are.
- **Rebuild:** `bash web/packages/website/design/fonts/subset.sh` (fonttools through `uvx`).
- **How the CSS uses them:** `design/fonts.css` (the `@font-face` rules, `unicode-range` per
  script, and the metric-matched fallback) and `design/README.md` (the type scale).
