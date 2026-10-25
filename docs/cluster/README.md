# Maktaba cluster

A Maktaba cluster is several computers on your local network working on one Maktaba
library together. One of them is the **home**; the others are **machines** that joined it.
They share the library on your NAS, they share the work, and you watch and manage all of
them from the home's web app as if they were one computer.

This folder explains how it works and how to run it:

| Page | What it covers |
|---|---|
| This page | What a cluster is, the home and the machines, how work is shared |
| [Join a machine](join-a-machine.md) | Step by step on macOS, Linux and Windows: install, mount the NAS, join, approve, run as a service; full nodes; a logon task or a service on Windows |
| [Updates](../updates.md) | How the home and every machine install each release by themselves, and what you control |
| [Update runbook](runbook-updates.md) | What to do when an update fails, failure by failure |
| [Files on the share](files-on-the-share.md) | The `maktaba` folder on the NAS: logs, metrics, jobs; how to read them from any machine; how long they are kept |
| [LLMs](llms.md) | Running Ollama or LM Studio on a machine and using it from the whole cluster |
| [Troubleshooting](troubleshooting.md) | What to do when something does not work |
| [Protocol](protocol.md) | The wire protocol between machines and the home, for developers |

## The home and the machines

**The home** is your main Maktaba computer (often a Mac next to the NAS). It runs:

- the web app and the API (port 8080),
- the database, which holds the library catalog and the **queues** of work (port 5432),
- the **hub**: the live centre of the cluster, which every Maktaba process on every machine
  stays connected to (an encrypted connection on which both sides prove who they are),
- its own workers.

**A machine** is any other computer — macOS, Linux or Windows — that you add to the cluster.
It runs Maktaba workers (`maktaba-pipeline`) and, as a **full node**, the whole of Maktaba
([below](#full-nodes-open-maktaba-on-any-machine)). It needs:

- the network path to the home (ports 8080 and 5432),
- the NAS share mounted, so it can read the videos and write next to them,
- your approval: a machine asks to join and shows a 6-digit code; you type that code on the
  home. Nothing joins without you.

You can add as many machines as you like, and remove or block any of them at any time.

## Full nodes: open Maktaba on any machine

A **full node** runs the same `maktaba-server` as the home, in node mode: its own web app and API
at its own address (port 8088), its workers, and the streaming role (videos on demand read from
the NAS on that machine; TV channels when the home places them there). The home stays the only
database, the only place migrations run, and the only holder of secrets (Plex tokens, Cloudflare
keys, accounts).

- **Open any machine.** Each machine's address is on its card on Admin > Machines. Its sign-in
  page offers **Continue with <home>**: accounts live on the home, so one sign-in works on every
  machine (`rules.cluster.sso`).
- **What answers where.** Library, video pages, collections and people are read on the machine
  itself (images through a local cache of the home's); playing a video reads the NAS there. Search,
  watch history and favourites, Processing, Machines, Logs, the admin settings, Extensions, Plex,
  Cloudflare, Users, Live TV changes and Updates go to the home through the machine (the machine
  proxies them, signed with its own key). Live TV plays through the home's front door. System
  health shows the machine's own processes and the cluster's.
- **When the home is away.** After 30 seconds without an answer the machine serves from its own
  copy of the home's database (Epic 37), read-only: sign-ins, reads, playback and TV go on; changes
  answer "the home is unreachable" until it is back.
- **The paused page.** A machine whose code does not fit the home's database (during a rollout,
  before its own update lands) shows **This machine is paused** with a link to the home, and
  reloads by itself once its update is in.

Every worker-only machine becomes a full node by itself at its next update; a new machine can be
installed as one directly ([join a machine](join-a-machine.md#a-worker-only-machine-becomes-a-full-node)).

## Updates

The home installs each release by itself in its quiet window, then every machine, one at a time;
the machines download it from the home and verify it themselves. See [updates](../updates.md) for
what happens and what you control, and the [update runbook](runbook-updates.md) when something
goes wrong.

## The cluster link

Story 35.16 moves what the machines say to the home onto encrypted connections that prove which
machine is talking:

- **A cluster CA on the home** (created at its first start) gives every machine a certificate:
  at join (the machine sends a certificate request; its key never leaves it) and, for a machine
  that joined before, by itself after its update. Certificates last 30 days
  (`rules.cluster.tls_cert_days`) and renew when a third is left.
- **One control channel per machine**: a WebSocket over mutual TLS on the home's API port (8080:
  plain HTTP is still served there). Control, commands, session checks and the routes the machine
  proxies to the home travel on it. Each data stream (TV between the front door and a streaming
  machine, release and wheelhouse downloads, backups) opens its own connection with a single-use
  ticket issued on the control channel.
- **Mode** (`rules.cluster.link_mode`): `prefer` by default (a machine with a certificate uses the
  link, one without uses the signed HTTP paths as before, so mixed versions keep working),
  switched to `require` by itself once every approved machine has been on the link for 24 hours.
  Revoking a machine closes its control channel and every stream at once.
- **Media** is never read through the link: every machine reads the library from the NAS itself.
- **Optional browser HTTPS** (`rules.cluster.tls_web_https`): each machine's web app also on port
  8443 with a certificate from the cluster CA (its fingerprint on the Machines page).

Certificates on each operating system, the database (`setup --pg-tls`), the modes and the
optional HTTPS are in [The cluster link](tls.md).

## How the work is shared: queues and pulling

Everything Maktaba does to a video is split into steps — read its details, extract the
audio, transcribe it, make subtitles, tag it, and so on. Each step has its own **queue** of
jobs, and all the queues live on the home.

Every worker on every machine **pulls** jobs from those queues: it takes the next job
waiting, does it, records the result, and takes the next one. Nobody hands work to a
machine. A fast machine simply pulls more often; a machine that is switched off simply stops
pulling, and its unfinished jobs go back to the queue for someone else.

Each machine pulls only from the queues it can do:

- a step that needs something the machine does not have is skipped (for example
  transcription needs an Apple Silicon Mac),
- a few steps always run on the home (the search index, thumbnails, review pages),
- the Plex steps (index, collections) run on a machine through the home, which adds the Plex
  token: the token never leaves the home (Admin > Machines > Relay; switch it off there and
  they run on the home only),
- a machine whose NAS is not reachable does not pull steps that read the videos,
- you can switch any queue off for any machine, or **drain** a machine (finish what it is
  doing, take nothing new), on **Admin > Machines**. Changes apply within a second or two.

## Watching the cluster

- **Processing** (`/queue`) shows every queue and every job running anywhere in the
  cluster, live, with the machine doing it.
- **Admin > Machines** shows each machine: whether it is online, what it can do, its NAS
  check and path map, the queues it pulls from, and live views of its processes — CPU,
  memory, GPU, the jobs they run, their log, and the LLM models on that machine.

The live views come from the hub over one connection per browser tab. If your network
blocks that kind of connection, the Processing page falls back to the older way of updating
and keeps working.

## Everything is also saved on the NAS

Every machine saves what it collects — logs, metrics, every job it ran, LLM usage — as
plain text files on the NAS, in a `maktaba` folder next to your videos, one folder per
machine (named by its IP address). The home also saves the queue and library numbers there.
Every machine can read every other machine's files, with a command or with any text tool.
See [Files on the share](files-on-the-share.md).

The files are the record. The home's database keeps the library and the queues, plus a
**cache**: the home copies the recent metrics and logs it receives into it so its pages read
fast (the last 24 hours by default). Only the home writes the cache, and emptying it loses
nothing — the home refills it from the files. The health numbers on the Processing page (NAS
speed, CPU, memory) and the transcription speed come the same way: the machines send them over
their live connection, and the home stores them.

## LLMs anywhere, usable everywhere

If a machine runs a local LLM (Ollama, LM Studio, …), the whole cluster can use it: Maktaba
on any machine, and your own tools through one address on the home,
`http://<home>:8080/api/cluster/llm/v1`, which speaks the OpenAI format. See [LLMs](llms.md).

## One Hugging Face login, on the home

The second listen of transcription uses a model you must accept on Hugging Face. Only the
home needs that login: it puts a copy of the model on the NAS, and the other machines load
that copy. See [The transcription model on the share](models.md).

## When the home is away

If the home's web app stops, the machines keep working on what the database gives them and
keep writing their files; they reconnect by themselves when it is back. If the whole home
is off (database included), the machines wait. See
[Troubleshooting](troubleshooting.md#the-home-is-unreachable--what-keeps-working).

## Keep the home's cluster key safe

The home has a **cluster key**, a small file that proves to every machine that it is the
home they joined: `~/.maktaba/cluster/home-cluster.key` of the user that runs the home's
server. It never leaves the home's disk. Back it up with your database: if it is lost (for
example when you reinstall the home without it), every machine has to
[re-join](troubleshooting.md#re-join-this-machine).

**With Docker Compose** (`deploy/compose/docker-compose.yml`) both of the home's keys live
on the `cluster_keys` volume (`/var/maktaba/keys`), so upgrading or recreating the
containers keeps them; back that volume up. Set `MAKTABA_CLUSTER_IP` to the host's LAN
address (a container only sees its own bridge address), so the home's files on the share
are filed under the address the machines know. The API writes its own folder under
`<share>/maktaba/cluster`, so its media volume is mounted read-write; it writes nothing
else there.

Beside it, the **home agent key** (`~/.maktaba/cluster/home-agent.key`, or the path in
`MAKTABA_HOME_AGENT_KEY`) lets the home's own workers join the live connection; the server
creates it at first start. It is readable by its owner only (mode `0600`). When the home's
workers run as another user than the server, as the Docker setup does (they share the key on
a volume and the server's group), set `MAKTABA_HOME_AGENT_KEY_MODE=0640` on the server: the
key is then also readable by the server's group. Only `0600` and `0640` are accepted; the
cluster key always stays `0600`.

## The long-term archive

The share keeps two weeks of files; the home keeps a compressed archive of every machine's
logs, jobs and LLM usage and of its own events for a year (up to 20 GB), on its own disk.
The Machines page reads it once the share has let a day go. Settings, on the home:
`MAKTABA_ARCHIVE_DIR` (default `~/.maktaba/archive`; with Docker, a volume),
`MAKTABA_ARCHIVE_KINDS` (default `logs,jobs,llm,events`), `MAKTABA_ARCHIVE_DAYS` (default
365; `0`: no age limit) and `MAKTABA_ARCHIVE_MAX_GB` (default 20). See
[Files on the share](files-on-the-share.md#the-archive-on-the-home).

## Ports at a glance

| From | To | Port | For |
|---|---|---|---|
| machines, browsers | home | 8080 TCP | the web app, the API, the live connection (WebSocket), the LLM address; the machines' [cluster link](tls.md) (TLS on the same port) |
| browsers | full node | 8088 TCP | the machine's own web app |
| browsers | any machine | 8443 TCP | the web app over HTTPS, when `tls_web_https` is on ([cluster link](tls.md#https-in-the-browser-optional)) |
| machines | home | 5432 TCP | the database (each machine has its own login) |
| machines | NAS | the NAS's (SMB 445, NFS 2049) | the videos and the `maktaba` folder |
| machines | home | 5353 UDP multicast | finding the home automatically (optional) |

## For developers

The design is in [specs/epics/31-multi-instance](../../specs/epics/31-multi-instance/README.md)
(phase 1: joining; [phase 2](../../specs/epics/31-multi-instance/design-agent.md): the
cluster channel, files and LLMs). The wire protocol is in [protocol.md](protocol.md).
