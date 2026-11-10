# NAS device checklists

The `nas-shapes` CI job (`.github/workflows/nas.yml`, `tests/nas/`) imitates in containers most
of what makes a NAS different: another uid, a read-only or foreign-owned share, a memory limit,
arm64, a kernel without Landlock. What a container cannot imitate is checked by hand on the
project's own devices (Epic 59, README Q6: one J4125 Synology, one Ryzen Synology, one N100 box
for TrueNAS; no self-hosted runner):

| Checklist | Device | What only a device shows |
|---|---|---|
| [synology.md](synology.md) | J4125 DS (Quick Sync), Ryzen DS (no GPU) | Container Manager's project install, DSM's ACL dialogs, real Quick Sync, the DSM kernel's Landlock result, disk sleep on DSM |
| [truenas.md](truenas.md) | N100 on TrueNAS SCALE 25.04 and 25.10 | the app install from the catalog, the Apps preset and ACL editor, ARC as available memory, VA-API on the N100 |
| [unraid.md](unraid.md) | any Unraid box (or a VM with a USB key) | the Community Applications template, the cache-pool data path, shfs media, user 99:100 |

## When

- before a platform's first release;
- after each DSM major (7.2 → 7.3 …) and each TrueNAS major (25.04 → 25.10 …);
- after a change to the platform's install files (`deploy/nas/`, `cloud/installers/nas/`) that
  CI cannot check, when the change's pull request says so.

## How

Each step says what to do and what to see. A step passes only when what you see matches. Take
the step's screenshots in the **dark theme**, once with the app in English and once in Arabic,
saved under `docs/nas/checklists/screenshots/` at the path the step lists (`synology/03-project.en.png`).

The result is a dated file in [results/](results/README.md): one per run, with `docker logs`
and the setup report attached. A failed step is a bug: open it with the result file linked.

## The format (checked by `tests/nas/check_checklists.py`, run by `nas.yml`)

- Front matter: `platform`, `devices`, and `status`: `draft` until the checklist has been run on
  its device once, then `ready`. A `ready` checklist must have every screenshot it names.
- Each step is a `### N. Title` heading followed by three lines: `- **Do:**`, `- **See:**` and
  `- **Screenshots:**` with an English (`.en.png`) and an Arabic (`.ar.png`) file of the same
  name, relative to `screenshots/`, or `- **Screenshots:** none` for a step with nothing to show
  in Maktaba (a terminal command, a DSM setting).
- Steps are numbered from 1 with no gap.
