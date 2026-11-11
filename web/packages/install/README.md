# maktaba.tv's install pages

Epic 59 story 59.11: `/install/docker` and `/ar/install/docker`. A household answers a few
questions (where it runs, the user, the memory, the data and media folders, hardware video, the
time zone, the network) and the page fills in the published Docker compose
(`cloud/installers/docker/docker-compose.yml`, built into the page) for them to copy or
download. Nothing is fetched and no answer leaves the page.

| File                           | What it is                                                                                                                  |
| ------------------------------ | --------------------------------------------------------------------------------------------------------------------------- |
| `src/compose.ts`               | the generator: the file's `# [name]` blocks turned on, the answers in place; `problems()` says what is wrong with an answer |
| `src/page.ts`                  | the form, in English or Arabic (`<html lang>`)                                                                              |
| `src/strings.ts`               | its text in both languages                                                                                                  |
| `site/`                        | the two pages and their stylesheet (dark by default)                                                                        |
| `../../vite.install.config.ts` | the build: `pnpm -C web build:install` writes `web/dist-install/`                                                           |

The tests (`src/*.test.ts`) run in web's vitest, on the real compose file. CI also runs every
block variant through `docker compose config` (`tests/nas/install_files.py`, nas.yml), so a
change to the compose file that breaks the page's rule fails there and here.

**The TrueNAS guide** (story 59.10): `/install/truenas` and `/ar/install/truenas`
(`site/install/truenas/`, `site/ar/install/truenas/`), static pages with no script: the
datasets and their ACL, the app (or the Custom App YAML until it is in TrueNAS's catalog), the
setup from the app's Shell, and what comes after. `src/guides.test.ts` keeps the two twins: the
same sections, the setup command, the placeholders, the Arabic page mostly Arabic.

## Screenshot placeholders

A TrueNAS screenshot can only be taken on the device. Until the device checklist
(`docs/nas/checklists/truenas.md`, step 13) has taken it, the guide shows a placeholder: a
`<figure class="placeholder" data-shot="screenshots/truenas/<name>.<lang>.png">` saying
**Screenshot to come**. Once the file is at `site/screenshots/truenas/<name>.<lang>.png`, the
figure becomes the image.

| `<name>`              | What it shows                                                      |
| --------------------- | ------------------------------------------------------------------ |
| `dataset-apps-preset` | Datasets → Add Dataset with the Apps preset chosen                 |
| `acl-entry`           | the ACL editor with the `apps` user's entry (Read)                 |
| `install-storage`     | the app's form: Storage (data host path, a media folder read-only) |
| `install-resources`   | the app's form: Resources (memory, GPU)                            |
| `install-via-yaml`    | Apps → Discover → Install via YAML with the Custom App pasted      |
| `app-shell`           | the app's Shell with the setup command                             |

TrueNAS's UI has no Arabic: the `.ar.png` files show TrueNAS in English with the Arabic page's
own words around them.

Hosting on maktaba.tv, and the other guides (`/install/nas`, `/install/synology`,
`/install/unraid`), are story 59.14's.
