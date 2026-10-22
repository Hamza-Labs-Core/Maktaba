# Troubleshooting the cluster

Find the message or the symptom you see. Most problems are one of three things: the
network between the machine and the home (ports), the NAS share on the machine, or the
machine's path map.

Three commands help everywhere (run them on the machine):

```sh
maktaba-pipeline cluster where                    # where this machine writes on the share, and whether it can
maktaba-pipeline cluster logs --level warning     # recent warnings and errors from every machine
maktaba-pipeline cluster events                   # the home's record of connections, refusals and commands
```

## No Maktaba home found on this network

`join` looks for the home with multicast (mDNS) for a few seconds. Many networks block
multicast: guest or "isolated" Wi-Fi, separate VLANs, VPNs, some mesh routers, virtual
machines and containers, or a firewall blocking UDP port 5353.

Give the home's address yourself:

```sh
maktaba-pipeline join --home http://192.168.50.20:8080
```

To find the home's address, look on the home: macOS **System Settings > Network**, Linux
`ip addr`, Windows `ipconfig`. Check the machine can reach it: open
`http://192.168.50.20:8080` in a browser on the machine — the Maktaba sign-in page should
appear.

## "The home is not accepting new machines"

**Accept new machines** on the home (**Admin > Machines**) is off — it turns itself off
after one hour. Turn it on and run `join` again.

If the Machines page says **This home can't take other machines**, the home runs on SQLite
or its database user cannot create logins. Machines need a PostgreSQL home whose database
user may create roles.

## The code is refused

- **Wrong code, n tries left** — check the digits against the machine's screen (the code
  is shown as `482 913`; type the six digits).
- **After five wrong codes** the request is rejected. Run `join` again on the machine; it
  shows a new code.
- **The request has expired** — a request lasts 15 minutes. Run `join` again.
- **The right code is refused every time** — the machine and the home did not see each
  other's keys. Something between them is changing the traffic (a proxy, or a device in the
  middle). Join the machine on a direct network path.

Other messages from `join`:

- **"The home already has this machine"** — it is already a member, or was revoked and not
  removed. On **Admin > Machines**, **Remove** the old record, then join again.
- **"…a request for this machine made with another key"** — an earlier request from a copy
  of this machine's settings is waiting. Reject it on the home (or wait 15 minutes), then
  join again.

## Firewall ports

| Port | Where it must be open | For |
|---|---|---|
| 8080 TCP | into the home, from the machines and from browsers | the web app, the API, the machines' live connection, the LLM address |
| 5432 TCP | into the home, from the machines | the database |
| 5353 UDP (multicast) | on the local network | finding the home automatically — optional, `--home` works without it |
| 445 TCP (SMB) or 2049 TCP (NFS) | into the NAS, from the machines | the videos and the `maktaba` folder |

Opening a port on the home:

- **macOS**: **System Settings > Network > Firewall > Options**: allow incoming connections
  for the Maktaba server (and for PostgreSQL).
- **Linux** (ufw): `sudo ufw allow from 192.168.50.0/24 to any port 8080,5432 proto tcp`
- **Windows** (PowerShell as administrator):
  `New-NetFirewallRule -DisplayName "Maktaba" -Direction Inbound -Protocol TCP -LocalPort 8080,5432 -RemoteAddress LocalSubnet -Action Allow`

PostgreSQL must also **listen** on the network and **accept** the machines:

- `listen_addresses = '*'` (or the home's LAN address) in `postgresql.conf`;
- a line in `pg_hba.conf` such as
  `host  maktaba  all  192.168.50.0/24  scram-sha-256`;
- with Docker, the database port published on the host (`5432:5432`).

Restart PostgreSQL after changing these.

## The machine joined but does not work

On **Admin > Machines**, look at the machine:

- **Offline** and never online — its worker is not running, or cannot reach the database
  (port 5432, above). Run `maktaba-pipeline run` in a terminal on the machine and read the
  error.
- **Online, but no live views** (no metrics, no log) — its live connection to the home is
  off. The worker still pulls jobs. Look at the worker's own log (its terminal or service
  log): if it says [this is not the home I joined](#this-is-not-the-home-i-joined) or
  [re-join this machine](#re-join-this-machine), see those sections. Otherwise port 8080 is
  blocked or a proxy drops WebSocket connections: check the port, and that any proxy passes
  the `Upgrade` and `Connection` headers.
- **A queue is off with a reason** — the reason says why (cannot run here, runs on the home
  only, storage not reachable, switched off, draining). That is usually expected.

## "This is not the home I joined"

The machine's log says **this is not the home I joined**. The machine reached something at
the home's address, but it could not prove it is the home this machine joined (it does not
hold the home's cluster key). The machine refuses it: no live views, no LLMs from other
machines. It keeps pulling jobs from the queues in the meantime.

The usual causes:

- **The home was reinstalled or moved, and its cluster key was not kept.** The key is the
  file `~/.maktaba/cluster/home-cluster.key` of the user that runs the home's server (or the
  file named by `MAKTABA_HOME_CLUSTER_KEY`). With Docker, it must be on a volume that
  survives the container.
- **The machine is talking to another Maktaba home**: two homes on the network, or a wrong
  `--home` address when it joined.
- **Something on the network is intercepting the connection.**

What to do:

1. If you have a copy of the old key file, put it back in the same place (readable only by
   the user that runs the server) and restart the home's server. The machines reconnect by
   themselves within a minute.
2. Otherwise, [re-join](#re-join-this-machine) every machine.
3. If the home was not reinstalled and the address is right, treat it as an intrusion: do
   not re-join over that network until you know what is answering.

Keep a backup of `home-cluster.key` with your database backups — without it, every machine
has to re-join.

## "Re-join this machine"

The machine's log says **re-join this machine**. It joined before this version of Maktaba
(its settings do not have the home's cluster key), or its home's cluster key was replaced.
It keeps pulling jobs, but has no live connection: the Machines page shows it without live
views, and it cannot use LLMs on other machines.

1. Stop the worker on the machine (stop its service).
2. On the home, **Admin > Machines**: **Revoke** the machine, then **Remove** it.
3. On the machine, delete its settings file (`node.json`; where it is:
   [Join a machine, Step 5](join-a-machine.md#step-5--on-the-home-approve-with-the-code)).
   On Windows, delete every `node.json` that is this PC's: `%APPDATA%\Maktaba\node.json` (a
   machine that joined before Epic 38) and `%LOCALAPPDATA%\Maktaba\node.json`. Maktaba reads
   the first and, without it, the second, so one left behind brings the old identity back. The
   node's uninstall script names them, and never names another PC's (see the next section).
4. On the home, turn on **Accept new machines**; on the machine run
   `maktaba-pipeline join` and approve the new code.
5. Start the worker again (or its service).

The machine comes back as a new record: if you had set its path map or switched queues off
for it by hand, set them again.

## "The machine settings … belong to another PC" (a Windows profile that roams)

A Windows machine that joined before Epic 38 keeps its node config in the roaming part of the
user's profile, `%APPDATA%\Maktaba\node.json`. Maktaba never copies or moves it, because an
older version of the install and the node's own scripts read it there. If that profile roams
(a domain roaming profile, or AppData redirected to a server), other PCs the user signs in to
see the same file.

Maktaba keeps them apart by itself:

- The file records the PC it belongs to (`machine_id`, Windows' id of the PC). The first new
  Maktaba that runs with a file that records none claims it for its PC.
- Every other PC skips a file that is not its own. It is no home's machine, so it never runs
  as that machine, and nothing on it changes the file.
- `maktaba-server` says so once at its start, and the app's setup shows it (*The machine
  settings in … belong to another PC*).

To have such a PC work for a home too, [add it as its own machine](join-a-machine.md). Its own
node config is then kept in `%LOCALAPPDATA%\Maktaba\node.json`, which stays on that PC, and the
other PC's file is left as it was.

## Storage not reachable

The machine's **Shared storage** check fails, and the queues that read videos show
*storage not reachable*.

1. Is the share mounted? macOS: Finder sidebar or `ls /Volumes`; Linux: `ls /mnt/nas`;
   Windows: File Explorer. Mount it (see [Join a machine, Step 3](join-a-machine.md#step-3--on-the-machine-mount-the-nas-share)).
2. Can the worker's user read the videos and write to the share? Try creating a file in a
   library folder as that user.
3. A worker running as a **Windows service** cannot see mapped drive letters: use
   `\\nas\share` paths and set the path map to them.
4. Is the **path map** right? (Next section.)

The check runs at start and every 5 minutes; restart the worker to check at once.

## The path map is wrong

The path map turns the home's video paths into this machine's. The machine works it out by
finding known videos; on unusual setups it can guess wrong or find nothing.

On **Admin > Machines**, open the machine, **Edit path map**, and set:

| Path on the home | Path on this machine (examples) |
|---|---|
| `/Volumes/Originals/` | Linux `/mnt/nas/` |
| `/Volumes/Originals/` | Windows `Y:/` or `//nas/originals/` |

Use the same ending (`/`) on both sides. A map you set is never replaced automatically;
**Reset to automatic** gives the job back to the machine. The worker uses the new map within
a minute.

## The Processing page does not update live

If the page shows that it is using the fallback, your browser could not keep a WebSocket
open to the home — usually a proxy or a security product. The page still works with the
older update stream (every 2 seconds) and retries the live connection every minute. Allow
WebSocket upgrades on `/api/cluster/ws` in the proxy.

## A machine was revoked

A revoked machine's database login is disabled at once, its live connection is closed, its
unfinished jobs go back to the queue and its worker stops; it does not restart by itself.

A worker stops for good (exit code 77) only when the home says so in a way that cannot be
faked: the home's sealed "revoked" message on the live connection, or the home's database
(the machine's record revoked or removed, or its database login refused). A connection that
is merely closed with "not a member" or "revoked" — which anyone posing as the home on the
network could send — is treated as a hint: the worker logs `cluster_agent_membership_doubt`,
asks the database at once, and keeps working if the machine is still approved there.

To bring the same computer back: on the home, **Remove** the revoked machine, then run
`maktaba-pipeline join` on the computer again and approve the new code.

## The home is unreachable — what keeps working

| If… | The machines | The files on the share | The web app |
|---|---|---|---|
| the home's web app / API is stopped, but its database runs | keep pulling and doing jobs; reconnect by themselves when it is back | keep being written by every machine | down until the API is back |
| the whole home is off (database too) | wait: they cannot take new jobs; jobs in progress may fail and go back to the queue later | keep being written | down |
| the NAS is off | stop pulling the queues that read videos; others go on | each machine writes to its own disk until the NAS is back | works; Machines shows the storage error |

While the home's API is away, machines can still use LLM models on their own machine, not
other machines'; and the live views are empty until it returns. When it does, everything
reconnects within a few seconds; nothing needs restarting.

To look at the cluster while the home is off, read the files from any machine:
`maktaba-pipeline cluster logs --follow` (see [Files on the share](files-on-the-share.md)).

## History is slow, or says `X-Maktaba-Source: files`

The home answers metrics and logs history from its database cache when the cache holds the
whole range, and from the files on the share otherwise; the `X-Maktaba-Source` header of
`/api/cluster/files/…` says which. `files` is normal for anything older than the cache
window (24 hours by default, `MAKTABA_CLUSTER_CACHE_HOURS`) and just after the home started
with an empty cache, while it refills from the files. The records are the same either way;
reading from files is only slower.

If recent history keeps coming from `files`, or the cache's **dropped** count grows (in the
processes list, `GET /api/cluster/processes` with an admin token, under `cache`), the home
could not write to its database for a while: the database was slow, full or stopped. Look at
the database. Nothing is lost — the files have every record. The home never answers from the
cache for a range it does not fully hold, so requests for that time come from the files
until it leaves the cache window. `MAKTABA_CLUSTER_CACHE_HOURS=0` turns the cache tables off
if you prefer to always read the files.

## Two machines write into one folder

The folder name is the machine's IP address. Machines behind a router of their own, in
virtual machines or in containers may all show the same address. Give each one a distinct
name with `MAKTABA_CLUSTER_IP` in its worker's environment (for example its real LAN
address).

## LLM requests fail

See [LLMs → When a request fails](llms.md#when-a-request-fails).
