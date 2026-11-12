# maktaba.tv's fonts

**Maktaba Text** is IBM Plex Sans Arabic (version 1.1, the copy `apps/apple` ships), subset
for the site. It sets both scripts. **Maktaba Display** is Amiri Bold (version 1.002), subset
for headlines (story 60.01 part B).

| File                               | Weight | Characters                                                     | Size  |
| ---------------------------------- | ------ | -------------------------------------------------------------- | ----- |
| `maktaba-text-400-latin.woff2`     | 400    | Basic Latin, Latin-1, general punctuation, arrows              | 14 KB |
| `maktaba-text-600-latin.woff2`     | 600    | the same                                                       | 15 KB |
| `maktaba-text-400-arabic.woff2`    | 400    | Arabic, its presentation forms, the bidi marks and isolates    | 26 KB |
| `maktaba-text-600-arabic.woff2`    | 600    | the same                                                       | 28 KB |
| `maktaba-display-700-latin.woff2`  | 700    | Basic Latin, Latin-1, punctuation (Amiri Bold)                 | 19 KB |
| `maktaba-display-700-arabic.woff2` | 700    | the Arabic letters, marks, digits and punctuation (Amiri Bold) | 38 KB |

- **Per page:** an English page downloads the three Latin files (49 KB); an Arabic page all six
  (142 KB), because its numbers, names and code are Latin. Epic 60's budget is 150 KB per page
  per language.
- **Hinting is dropped** (`--no-hinting`, a quarter of the size). Current browsers on every
  system render the outlines without it.
- **Licence:** [`OFL.txt`](OFL.txt) (Plex) and [`OFL-Amiri.txt`](OFL-Amiri.txt) (Amiri), the
  SIL Open Font License 1.1. The Amiri subset is renamed "Maktaba Display" the same way. The licence reserves the name
  "Plex" for the original, and a subset is a Modified Version, so the files are renamed
  "Maktaba Text" (name table and CSS family). Their description field says what they are.
- **Rebuild:** `bash web/packages/website/design/fonts/subset.sh` (fonttools through `uvx`). Its output is
  not byte-for-byte reproducible (the files carry a build timestamp), so commit only the files
  a change meant to rebuild.
- **How the CSS uses them:** `design/fonts.css` (the `@font-face` rules, `unicode-range` per
  script, and the metric-matched fallback) and `design/README.md` (the type scale).
