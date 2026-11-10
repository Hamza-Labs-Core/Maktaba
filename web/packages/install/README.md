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

Hosting on maktaba.tv, and the other guides (`/install/nas`, `/install/synology`,
`/install/truenas`, `/install/unraid`), are story 59.14's.
