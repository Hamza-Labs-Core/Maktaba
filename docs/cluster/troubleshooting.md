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
- **Online, but no live views** (no metrics, no log) — its live connection to the home
  (port 8080) is blocked or a proxy drops WebSocket connections. The worker still pulls
  jobs. Check the port, and that any proxy passes the `Upgrade` and `Connection` headers.
- **A queue is off with a reason** — the reason says why (cannot run here, runs on the home
  only, storage not reachable, switched off, draining). That is usually expected.

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

## Two machines write into one folder

The folder name is the machine's IP address. Machines behind a router of their own, in
virtual machines or in containers may all show the same address. Give each one a distinct
name with `MAKTABA_CLUSTER_IP` in its worker's environment (for example its real LAN
address).

## LLM requests fail

See [LLMs → When a request fails](llms.md#when-a-request-fails).
