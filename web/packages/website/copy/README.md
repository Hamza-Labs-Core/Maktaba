# maktaba.tv's copy

- [`voice.md`](voice.md): the voice guide, in English and in Arabic (story 60.02 part A). Every
  page on the site follows it.
- `<page>.en.md` and `<page>.ar.md`: each page's copy, written separately in each language
  (part B), with the same `##` keys (`design/tests/mockups.test.mjs` checks). Each pair is that
  page's strings table: `home` (with the chosen tagline), `download`, and
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
| The tagline (part B)                                    | English  | the owner   | 2026-11-12 | 59efc591c | proposal 1, chosen (Q1)        |
| The tagline (part B)                                    | Arabic   | the owner   | 2026-11-12 | 59efc591c | proposal 1, chosen (Q1)        |
| Home and download copy (`home.*.md`, `download.*.md`)   | English  |             |            |           |                                |
| Home and download copy (`home.*.md`, `download.*.md`)   | Arabic   |             |            |           |                                |
| The front door's lines (`front-door.*.md`)              | English  |             |            |           |                                |
| The front door's lines (`front-door.*.md`)              | Arabic   |             |            |           |                                |

The Arabic home, download and front-door copy waits for the owner's own read (Q2): approving
part B's design on 2026-11-12 approved the look and the tagline, not a read of every Arabic text.

## The tagline

Chosen on 2026-11-12 (Q1), proposal 1 of five in each language:

- English: "Your own library, on every screen at home."
- Arabic: «مكتبتكم، على كل شاشة في البيت.»

Not chosen, kept here only as the record (their copy, mockups and renders were removed):
"Everything your family watches, kept at home." / «ما تحبون مشاهدته، محفوظٌ في بيتكم.»;
"A quiet home for your films and series." / «أفلام البيت ومسلسلاته، في مكان واحد هادئ.»;
"Your library speaks Arabic and English." / «مكتبةٌ تتكلم لغتكم.»;
"Find the moment a word is said." / «ابحثوا بالكلمة، تجدوا اللحظة.»
