# Contributing to Maktaba

Maktaba (مكتبة, "library") is a self-hosted media platform: it turns a folder tree of
video files into a streamable library where every spoken word is searchable, with
first-party apps for web, phones, desktops and TVs. Arabic is first-class throughout.

These guides are for people working on Maktaba itself: the services, the apps, the tests
and CI. The source lives at
[github.com/Hamza-Labs-Core/Maktaba](https://github.com/Hamza-Labs-Core/Maktaba).

## Guides

- [Architecture overview](architecture.md): the services, the database, and how they
  talk to each other.
- [Repository layout](repo-layout.md): the top-level folders and what lives in each.
- [Local setup](local-setup.md): prerequisites and getting a dev stack running.
- [Running the tests](tests.md): each test suite, the tiers, and the affected-tests gate.
- [Conventions](conventions.md): English and Arabic together, tests with the code,
  server-side roles, no secrets, commit style.
- [Problem types](problem-types.md): the API's errors, their registry, how to add one, and
  the checks that keep the API, the apps and maktaba.tv in step.
- [API levels](api-levels.md): how a home and its apps tell whether they can work together,
  the client floor, the TV platform names, and how to change a response safely.
- [Parental controls](parental-controls.md): for client and API authors: what a player must
  do, the routes and their codes, the route registry, and how to run the role table and the
  bypass rehearsal.
- [Playback: profiles, device reports and signed URLs](playback.md): for player and TV app
  authors: how the home picks direct, remux or transcode, what `device_caps` reports, and the
  signed URLs a TV fetches.
- [CI](ci.md): how CI picks the jobs a change needs, and the single merge gate.
- [Pull requests](pull-requests.md): branch, test, describe, review, merge.
- [Maktaba Cloud: how it stays up](cloud.md): the two nodes, deploys, backups, and the cloud rehearsal.
- [Maktaba on a NAS: how it fits together](nas.md): one image for any user, what the server
  finds out for itself, the install files, maktaba.tv's guides, the strings and the tests.
- [Maktaba on a NAS: the TrueNAS app](nas-apps.md): the catalog app, its CI with the catalog's own tooling, and how releases move it.
- [maktaba.tv: how the website is built and deployed](website.md): the build, the Worker, the deploy with its preview smoke, going live, and rolling back.
- [Security rules for plugins and contributors](../security/conformance.md): what
  plugins and contributors must follow, and how to report a vulnerability.

## The short version

```sh
git clone https://github.com/Hamza-Labs-Core/Maktaba.git
cd Maktaba
cp .env.example .env
make prereqs      # tells you which tools are missing
make dev          # live-reload stack
make test         # unit tier: no network, no sudo
```

CI runs the same `make` targets you run locally, so a green `make lint` and `make test`
on your machine means the lint and unit gates will be green too.

## Reporting a security problem

Never open a public issue for a vulnerability. Email security@maktaba.dev and see the
[reporting policy](https://maktaba.dev/security/policy/).
