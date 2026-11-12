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

## The NAS guides (story 59.14)

| Page                       | What it is                                                                                                                                |
| -------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| `/install/nas`             | which guide to follow, the model table (`src/nas-models.json`, read by `src/nasModels.ts`, drawn by `src/nasPage.ts`) and what to prepare |
| `/install/unraid`          | the Community Applications template (`deploy/nas/unraid/maktaba.xml`)                                                                     |
| `/install/qnap`            | Container Station, with the file from `/install/docker`; not yet tried on a device                                                        |
| `/install/ugreen`          | UGOS Pro's Docker and ASUSTOR's Portainer, with the file from `/install/docker`                                                           |
| `/install/after-setup`     | permissions, quiet hours, speech to text, hardware video, proxy and firewall, updates, a computer with a GPU (one `id` per section)       |
| `/install/troubleshooting` | one section per message (`#write-denied`, `#port-taken`, `#no-access`, `#render-device`, `#plugins-held`, `#not-enough-memory`, ...)      |

Each has its Arabic page under `/ar/`. `src/twins.test.ts` keeps every pair mirrored (the same
sections and ids, the same placeholders, links both ways). The **model table** is data, checked
once per release cycle (its `checked` date); Maktaba itself never trusts it.

**Arabic:** the build (`vite.install.config.ts`) fails when an Arabic page's prose is more than
20% Latin: maktaba.dev's own check (`../devportal/scripts/parity.mjs`, through `src/latin.ts`),
with two kinds of names counted as code: the model table, and a NAS menu name kept in English in
«quotes» after an Arabic description (a NAS with no Arabic UI). The pages a script fills
(`docker`, `synology`) are checked on what their script renders, in `twins.test.ts`.

**In a browser** (`pnpm -C web e2e:install`, `web-ui.yml`'s `install` job): the install page's
file equals the generator's for the same answers and no request carries an answer (T04); every
guide is right to left in Arabic and dark, and is photographed in both languages
(`test-results/guides/`, the run's `install-guides-screenshots` artifact) (T05).

**NAS screenshots still to come**, by hand on each device checklist (`docs/nas/checklists/`):
`screenshots/unraid/{template,console}`, `screenshots/qnap/create-application`,
`screenshots/ugreen/project`, each `.en.png` and `.ar.png`, beside TrueNAS's above and
Synology's (`src/synologyGuide.ts`).

**Hosting:** the site builds to `web/dist-install/` (its hashed files under `install/assets/`),
and maktaba.tv's build (`web/packages/website`, Epic 60 story 60.03) copies it to the root of
the website, at `/install/` and `/ar/install/`. `website-deploy.yml` deploys it with the rest of
the site.
