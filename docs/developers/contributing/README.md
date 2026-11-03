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
- [CI](ci.md): how CI picks the jobs a change needs, and the single merge gate.
- [Pull requests](pull-requests.md): branch, test, describe, review, merge.
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
