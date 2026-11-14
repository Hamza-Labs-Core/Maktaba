# Story 54.49 — Maktaba plugins on homes with no internet

> Epic 54 · Phase 5 · Depends on: 54.45, 54.46, 54.10, Epic 51 (51.16) · Not built

## Why

A home that never reaches the store must still be able to add, update and remove Maktaba's plugins.

## Description

- **Files.** Each Maktaba release publishes its plugins' packages as files beside the installers, and one offline file per platform that holds all of them with the assets they require, built by the offline-file code of 51.16.
- **Install.** Install from a file accepts a Maktaba package as it accepts a store-signed file (README §6.4): the signatures are checked offline against the trust root in the server. It is never "unreviewed" and never asks for the typed name.
- **Updates.** The offline file of a newer release updates the server's plugins with it; one plugin's newer package can be installed alone when its `maktaba` range takes the running server.
- **Withdrawn plugins.** The home shows how old its list of withdrawn plugins is, as it does today; plugins keep running.

## Tests

- With the network denied (the egress harness of 54.03), a Core home installs a Maktaba plugin and its assets from files and runs its stage.
- A file with a broken signature is refused with `plugin_signature_invalid` and nothing is written.
