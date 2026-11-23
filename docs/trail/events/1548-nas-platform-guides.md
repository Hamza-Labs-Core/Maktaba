---
schema: 1
kind: pr
pr: 1548
title: "A full guide for each NAS the code supports, each model routed to its guide, and QNAP's platform written"
date: 2026-11-23
session: a1e17a40-6168-4a7f-90a0-877ab55b9b71
agent: claude-code
prs: [1548, 1546, 1140]
---

## What happened
- A new requirement: every supported NAS platform needs a complete step-by-step guide. The work was stacked on #1546.
- **Which platforms the code supports** (`hostinfo.Platform*`, the install files, the specs):
  - Synology, TrueNAS and Unraid have their own install files.
  - QNAP has the `qts` platform.
  - UGREEN and ASUSTOR run the plain compose file.
  - OpenMediaVault is only a name and a `/srv/` share rule, with no install file, CI cell or story.
  - TerraMaster and CasaOS/ZimaBoard appear nowhere.
  - Guides were written for the supported six. The others are reported, not guessed at.
- **Stale guide docs found and fixed:**
  - The QNAP and combined UGREEN/ASUSTOR pages lacked ports, hardware video, roles, updates and troubleshooting.
  - UGREEN's user menu was wrong. It is Control Panel → User Management → User → Add, not "Control Panel → User → Create".
  - Neither page said that QTS and ADM admit only administrators over SSH.
  - The website's `/install/` page still called the TrueNAS app "a custom app".
- **A code gap behind the docs:** the install page wrote `MAKTABA_PLATFORM: docker` for a QNAP. So 59.13's QNAP share rule (`/share/<name>`, from #1140) never applied to a home made from maktaba.tv, although `hostinfo` says the NAS compose files set the platform. It now writes `qts`.
- Each vendor fact was checked against the maker's own documentation by a research subagent, with URLs. Anything only a device can confirm went into the new `qnap`, `ugreen` and `asustor` checklists:
  - `/dev/dri` in a compose file on QNAP and ASUSTOR;
  - Portainer's 19943 on ADM;
  - whether `docker` needs `sudo`.
- The NAS page's model table now links each model to its maker's guide (`GUIDE` in `nasPage.ts`).

## What we learned
- A page that claims a platform can drift from the code that names it. The guides said QNAP worked like any Docker host, while the server had QNAP-specific rules the install page never switched on.
- Vendor docs corrected wording that had been written from memory: UGREEN's menus, the SSH limits, and the owner of QNAP's 8081 (the optional Web Server package, not QTS).
- A PR whose base is not `main` runs no CI here (`ci.yml` runs on pull requests to main and `release/**`). A stacked docs PR gets its checks only after it is retargeted.

## Keep doing
- Checking "is this platform supported?" against `hostinfo.Platform*` and the install files before writing a guide for it.
- Sending vendor-UI facts to a research pass with sources, and leaving unconfirmed ones as device checklist items.

## Stop doing
- Writing NAS menu paths from memory.

## Open
- Every QNAP, UGREEN and ASUSTOR checklist step needs a device. The project has none.
- Screenshots: `qnap/create-application`, `ugreen/project`, `asustor/stack`.
- OpenMediaVault: the code names it, but nothing installs it. A guide waits for an install file and a CI cell.
