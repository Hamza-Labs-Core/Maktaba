# Maktaba on a NAS: the install files

Epic 59 ([specs/epics/59-nas](../../specs/epics/59-nas/README.md)). Every NAS runs the same
image, `ghcr.io/hamza-labs-core/maktaba-server`, as a non-root user, on the host network, with
its data on local storage and each media folder at its own path.

| NAS | How | File |
|---|---|---|
| Any Linux with Docker, QNAP (Container Station), UGREEN (Docker in App Center), ASUSTOR (Portainer), Portainer or Dockge stacks | the published Docker compose, filled in at [maktaba.tv/install/docker](https://maktaba.tv/install/docker/) | [`cloud/installers/docker/docker-compose.yml`](../../cloud/installers/docker/docker-compose.yml) (the base) |
| Unraid | a Community Applications template | [`unraid/maktaba.xml`](unraid/maktaba.xml), [its README](unraid/README.md) |
| Synology (59.09), TrueNAS (59.10) | made from the base | `synology/`, `truenas/` (their stories) |

**The base.** The published compose file's optional settings are commented blocks headed
`# [name]`: `memory` (`mem_limit` by tier: 4g Light, 6g Standard, 16g Strong), `bridge` (the
ports and `MAKTABA_HOST_IP`, and no `network_mode: host`), `dbus` (the host's Avahi), `dri`
(`/dev/dri` and the render group) and `nvidia`. Turning a block on removes the `# ` in front of
its settings; release rewrites `:latest` to the release's tag (`maktaba-docker-compose.yml`).
The install page (`web/packages/install`) and the checks (`tests/nas/install_files.py`) use the
same rule.

**QNAP.** Container Station → Applications → Create, paste the file. The data folder is a share
folder such as `/share/Container/maktaba`; the user is one made for Maktaba (`id <user>` over
SSH gives its uid and gid). QTS keeps port 8080 for its own UI: Maktaba moves its cluster port
at the start and setup shows the new one (59.08), kept in `server.toml`. Untested on a QNAP
device so far (the device checklist covers it when one is available).

**UGREEN (UGOS Pro).** Docker → Project → Create, with the project folder under
`/volume1/docker/maktaba` and the file pasted. An N100 model (DXP4800 Plus) has VA-API: turn
the `dri` block on with the render group's id.

**ASUSTOR (ADM).** Portainer from App Central, then Stacks → Add stack, the file pasted. Data
under `/volume1/Docker/maktaba`.

**Portainer, Dockge.** The file works as a stack unchanged.

**What CI checks** (nas.yml): the base with no block, each block and every block
(`docker compose config`), the Unraid template's schema and its render to `docker run`
arguments, the template against the base (image, network, stop timeout, data path, render
device, environment), and the development compose's images (`deploy/compose`, development
only) resolve under release's names. The `compose-plain` cell starts the filled-in base with
`docker compose`; the `uid-unraid` cell starts the template's `docker run`, as 99:100.
