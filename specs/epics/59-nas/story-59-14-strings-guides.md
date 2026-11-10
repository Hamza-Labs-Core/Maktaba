# Story 59.14 — English and Arabic: setup screens, messages, and the install guides on maktaba.tv

> Epic 59 · Phase 4 (strings land with each story) · Depends on: 59.01–59.13

## Why

Every user-facing string ships in English and Arabic, in the web and in the native apps. This
epic adds many such strings:
- setup steps;
- permission states and fixes in each NAS's own words;
- hardware tiers;
- isolation levels;
- update messages.

It also adds install guides that a NAS owner will follow step by step. maktaba.tv is where
households install and learn Maktaba. maktaba.dev is the developer wiki.

## Description

**Strings.**
- Each story's strings table is the source. Keys go into the web's catalogs (`en.json`,
  `ar.json`), the server's message catalog (for container-start messages printed before the web
  is up), and the plugin i18n files where a plugin's text changes.
- Container-start messages (59.01, 59.08) print English, then Arabic, on two lines. They are
  read in a NAS's log viewer, which cannot switch language.
- **NAS menu names:** DSM and TrueNAS names (Control Panel → Shared Folder; Datasets →
  Permissions) are given in Arabic as the Arabic DSM UI shows them where DSM has an Arabic UI
  (**to check** on a DSM set to Arabic). Otherwise they stay as the English name in quotes after
  an Arabic description.
- The existing parity check (`web/packages/devportal/scripts/parity.mjs` and the web catalogs'
  key-parity test) fails a missing Arabic key.

**The guides on maktaba.tv** (`/install/nas`, `/install/synology`, `/install/truenas`,
`/install/unraid`, `/install/docker`, each with `/ar/…`):
- **Before you start:**
  - a model table: the CPU, whether video uses hardware, the tier at the RAM it ships with, and
    "Fits / Fits with more memory / Use as storage";
  - what to prepare: the user, the folders, an SSD if there is one.
- **The install page:** a short form (NAS type, model or CPU, RAM, media folders and read-only or
  not, time zone, host IP) that produces the filled compose file or template (59.09–59.11).
  Nothing is sent anywhere; it is built in the page.
- **Step by step** with dark screenshots in English and in Arabic (DSM and TrueNAS in their Arabic
  UI where it exists).
- **After setup:**
  - permissions;
  - quiet hours;
  - speech to text and the measured speed;
  - hardware video and the render group;
  - reverse proxy and firewall;
  - updates (self-update and catalog);
  - adding a GPU computer as a node.
- **Troubleshooting**, each item linked from the message that shows it:
  - write denied;
  - port taken;
  - No access;
  - render device not open;
  - plugins held;
  - not enough memory.

**The wiki** (`docs/wiki/epics/epic-59-nas.md`, with its Arabic in `docs/wiki/db/ar/epic-59.json`)
carries the developer view. `docs/hardware.md` gains the NAS tiers, and `docs/updates.md` the
NAS notes.

## Acceptance criteria

- **Given** the web catalogs, **then** every key added by Epic 59 exists in both, and the parity
  test passes.
- **Given** each guide page, **then** it exists in English and Arabic, mirrored, with screenshots
  in both, and the Arabic page passes the Latin-share check.
- **Given** a container-start message, **then** the log shows it in both languages.
- **Given** the install page, **then** no answer leaves the browser (no request carries them).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | web unit | catalogs | parity | all Epic 59 keys in both |
| T02 | site build | guides | build + parity.mjs | both languages; Latin share under the limit |
| T03 | Go unit | container messages | render | two lines, English then Arabic |
| T04 | Playwright | install page | fill and generate | compose matches fixture; no network request with answers |
| T05 | Playwright | guides, `dir=rtl` | screenshot dark | mirrored layout |

## Strings

The strings tables of 59.01–59.13.

## Edge cases

- **A DSM set to a language other than English or Arabic:** the guide's menu names are given in
  English with a note.
- **Model table drift:** the table is data (`site/data/nas-models.json`), checked once per release
  cycle. Maktaba itself never trusts it (59.04 detects).

## Build plan

| File | Change |
|---|---|
| `web/…/i18n/{en,ar}.json` | Epic 59 keys |
| `cmd/maktaba-server/internal/messages/` | bilingual container messages |
| maktaba.tv site: `/install/*` pages, `nas-models.json`, the install page | guides |
| `docs/wiki/epics/epic-59-nas.md`, `docs/wiki/db/ar/epic-59.json` | wiki |
| `docs/hardware.md`, `docs/updates.md` | NAS sections |
