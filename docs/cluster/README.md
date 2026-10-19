# Maktaba cluster

A Maktaba cluster is several computers on your local network working on one Maktaba
library together. One of them is the **home**; the others are **machines** that joined it.
They share the library on your NAS, they share the work, and you watch and manage all of
them from the home's web app as if they were one computer.

This folder explains how it works and how to run it:

| Page | What it covers |
|---|---|
| This page | What a cluster is, the home and the machines, how work is shared |
| [Join a machine](join-a-machine.md) | Step by step on macOS, Linux and Windows: install, mount the NAS, join, approve, run as a service |
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
It runs only Maktaba workers (`maktaba-pipeline`). It needs:

- the network path to the home (ports 8080 and 5432),
- the NAS share mounted, so it can read the videos and write next to them,
- your approval: a machine asks to join and shows a 6-digit code; you type that code on the
  home. Nothing joins without you.

You can add as many machines as you like, and remove or block any of them at any time.

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

## When the home is away

If the home's web app stops, the machines keep working on what the database gives them and
keep writing their files; they reconnect by themselves when it is back. If the whole home
is off (database included), the machines wait. See
[Troubleshooting](troubleshooting.md#the-home-is-unreachable--what-keeps-working).

## Keep the home's cluster key safe

The home has a **cluster key**, a small file that proves to every machine that it is the
home they joined: `~/.maktaba/cluster/home-cluster.key` of the user that runs the home's
server (with Docker, keep it on a volume). It never leaves the home's disk. Back it up with
your database: if it is lost (for example when you reinstall the home without it), every
machine has to [re-join](troubleshooting.md#re-join-this-machine).

## Ports at a glance

| From | To | Port | For |
|---|---|---|---|
| machines, browsers | home | 8080 TCP | the web app, the API, the live connection (WebSocket), the LLM address |
| machines | home | 5432 TCP | the database (each machine has its own login) |
| machines | NAS | the NAS's (SMB 445, NFS 2049) | the videos and the `maktaba` folder |
| machines | home | 5353 UDP multicast | finding the home automatically (optional) |

## For developers

The design is in [specs/epics/31-multi-instance](../../specs/epics/31-multi-instance/README.md)
(phase 1: joining; [phase 2](../../specs/epics/31-multi-instance/design-agent.md): the
cluster channel, files and LLMs). The wire protocol is in [protocol.md](protocol.md).
