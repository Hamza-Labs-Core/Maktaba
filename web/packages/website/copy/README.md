# maktaba.tv's copy

- [`voice.md`](voice.md): the voice guide, in English and in Arabic (story 60.02 part A). Every
  page on the site follows it.
- `<page>.en.md` and `<page>.ar.md`: each page's copy, written separately in each language
  (part B), with the same `##` keys (`design/tests/mockups.test.mjs` checks). Each pair is that
  page's strings table: `home` (with the five tagline proposals), `download`, and
  `front-door` (Phase 1's front door, rewritten in the voice).

## Approvals

The lead reviews every text for the voice. The Arabic, and part B's tagline, are approved by the
owner, who reads every Arabic page (Epic 60 Q1, Q2). Each approval is recorded here with
its date and the commit it approves.

| What                                                    | Language | Approved by | Date       | Commit    | Notes                          |
| ------------------------------------------------------- | -------- | ----------- | ---------- | --------- | ------------------------------ |
| The voice guide (`voice.md`)                            | English  | the owner   | 2026-11-11 | fb0cb05a3 |                                |
| The voice guide (`voice.md`)                            | Arabic   | the owner   | 2026-11-11 | fb0cb05a3 | the household plural («بيتكم») |
| The part A mockups' copy (`../design/mockups/make.mjs`) | English  | the owner   | 2026-11-11 | fb0cb05a3 |                                |
| The part A mockups' copy (`../design/mockups/make.mjs`) | Arabic   | the owner   | 2026-11-11 | fb0cb05a3 |                                |
| The tagline (part B)                                    | English  |             |            |           |                                |
| The tagline (part B)                                    | Arabic   |             |            |           |                                |
| Home and download copy (`home.*.md`, `download.*.md`)   | English  |             |            |           |                                |
| Home and download copy (`home.*.md`, `download.*.md`)   | Arabic   |             |            |           |                                |
| The front door's lines (`front-door.*.md`)              | English  |             |            |           |                                |
| The front door's lines (`front-door.*.md`)              | Arabic   |             |            |           |                                |
