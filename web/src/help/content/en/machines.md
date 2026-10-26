# Machines

Let other computers share the work of your Maktaba: they pull jobs from the same queues, read the same files and serve the same library. Only administrators see this page.

## What it is {#what-it-is}

Your Maktaba runs on one computer, **the home**. It keeps the library, the database and the accounts. Other computers can join it as **machines**. A joined machine pulls jobs (transcription, reading files, Plex work and so on) from the home's queues, so a large library is processed faster.

[Administration → Machines](/admin/machines) shows the home, the machines asking to join, the joined machines and what each one is doing now. The page refreshes by itself every few seconds; the badge next to the title (**Live**, **Connecting…**, **Reconnecting…**) says whether the live view is connected.

The page has these tabs:

- **Machines**: the home, join requests, joined machines, the extension relay card and **Running now** (each connected machine's processor, memory, disk, network and processes).
- **Jobs**: job events across machines, with the last 10 minutes' totals. Filter by machine, queue and outcome.
- **Queues**: how many jobs wait and run in each queue over time.
- **Library**: library totals and hours transcribed over time.
- **Logs**: the log of any machine or process, live or for an earlier day. See [Logs](/help/logs).
- **LLM**: the language models the machines run, their requests, and **Try a model**.
- **Relay**: the extension relay (below).

## How to use it {#how-to-use}

### Add a machine {#add-a-machine}

1. On the home's card, turn on **Accept new machines**. It stays open for one hour (**Open for another …**).
2. Install Maktaba on the other computer as a machine and point it at the home. It asks to join and shows a 6-digit code.
3. On the home, the machine appears under **Asking to join**. Type the code shown on it in **Code shown on …** and click **Approve**. You have 15 minutes and 5 tries.
4. Click **Reject** for a machine you do not know.

Once approved, the machine appears under **Joined machines** and starts pulling jobs.

### Choose the queues a machine pulls from {#choose-queues}

Each joined machine has a table **Queues this machine pulls from**. For each queue, the **This machine** column says what it really does (**Pulls from it**, or **Doesn't pull from it** with the reason). The switch is your choice:

- **Automatic**: the machine decides from what it can run.
- **On**: pull from this queue.
- **Off**: never pull from this queue on this machine.

### Drain a machine {#drain}

Turn on **Drain: finish current work, take nothing new** before you restart, update or move a machine. It finishes the jobs it has and takes no new ones; its status shows **Draining**. Turn it off to let it work again.

On **Running now** each process also has **Drain** and **Resume**, **Reload queues**, **Log level** and **Disconnect** (it reconnects by itself unless it is stopped).

### Revoke or remove a machine {#revoke}

**Revoke** cuts a machine off at once: it loses access to the database and its unfinished jobs go back to the queue. Its record stays, with the status **Revoked**. **Remove** (on a revoked or rejected machine) deletes the record; the machine can ask to join again later.

### Shared storage and path maps {#path-map}

Every machine must read the same files. Maktaba stores a file on a network share as `smb://server/share/...` and each machine maps that share to the folder where it is mounted locally.

- **Shared storage** shows the last check: **Read: ok** / **Write: ok**, or **failing**, per share.
- **Share → local mount** lists the mapping. **worked out automatically** means the machine found its own mounts; **set by you** means you edited it.
- Click **Edit mounts** to change it (**Add a share**, one row per share, then **Save**). **Reset to automatic** drops your rows.

### Storage servers {#storage-servers}

On the home's card, **Storage servers** lists each NAS by the **Name used in paths** and the **Addresses** it is reached by. If the NAS gets a new IP address, edit it here once. The setting for names with `" : ? * < > |` matches how the NAS stores such names over SMB.

### Canonical paths {#canonical-paths}

The home converts the stored file paths to the shared `smb://server/share/...` form by itself, once every share is mounted and a check passes. The **Canonical paths** panel shows where it is. **Check now** asks it to look at once, **Revert last conversion** puts the old paths back (automatic conversion then waits), and **Convert automatically again** ends that wait. The settings are in [Settings → Rules](/help/rules#storage).

### Extension relay {#relay}

Machines run the Plex stages through the home. The home adds the Plex token, so the token never leaves the home. Turn it on or off with **Relay extension calls for machines** on the **Relay** tab. Off: the Plex stages run on the home only. The limits per machine are in [Settings → Rules](/help/rules#relay).

### The same account on every machine {#one-sign-in}

Each machine has its own web address (linked on its card). Sign in there with your usual username and password: accounts always live on the home, and the machine checks them with it.

### The paused page {#paused-page}

When a machine cannot serve the app, its web address shows a full page instead: **This machine is paused** (its version does not fit the home's) or **This machine cannot reach its home**. It names the reason, links to the home's address, and reloads by itself when the machine is ready. **Try again** reloads it now.

## What the states and messages mean {#states}

### Machine status {#machine-states}

- **Online**: connected and working.
- **Offline**: not seen in the last minute. **last seen …** says when.
- **Draining**: finishing its jobs and taking no new ones, because you turned on drain.
- **Paused**: connected but takes no jobs because its version and the home's do not fit. See [The home is behind](/help/machines#home-behind) and [This machine is behind](/help/machines#machine-behind).
- **Revoked**: cut off by you. **Remove** deletes the record.
- **Rejected**: you rejected its join request. **Remove** deletes the record.
- **Full machine**: also serves the web app and video.
- **Worker only — re-run the installer once (install-node … -Full)**: only pulls jobs; see [Worker only](/help/machines#worker-only).
- **Expired** (on a join request): the request ran out of time. Ask again from the machine.

### Why a machine does not pull from a queue {#queue-reasons}

- **not in this worker's stage list**: this worker process was started for other queues.
- **a newer version is in the database**: the queue's code on the home is newer than this machine's. Update the machine.
- **cannot run here**: the machine lacks what the queue needs (for example a GPU); the reason follows.
- **runs on the home only**: this queue never runs on a machine.
- **storage not reachable**: the machine's share check failed, so it skips queues that read or write files.
- **switched off for this machine**: you set the switch to **Off**.
- **draining**: the machine is draining.
- **paused**: the machine is paused (version mismatch).
- **machine overloaded**: it is busy; it takes more when it has room.
- **not given to this machine**: the work is assigned to another machine.

### Canonical paths {#canonical-states}

- **Every stored path on a share is canonical.** / **Nothing to convert**: done; nothing to do.
- **Waiting: …**: a share is not mounted or the check did not pass. **Why it waits** lists the problems. The home looks again by itself.
- **Backing up…**, **Pausing the stages…**, **Converting…**, **Checking…**: a conversion is running. The stages that use the shares pause meanwhile; the rest keeps running.
- **The last conversion stopped.**: the home tries again later; nothing is lost.
- **Automatic conversion is off** / **on hold after a revert**: it waits for you.

## FAQ {#faq}

### Do machines need their own accounts or libraries? {#faq-accounts}

No. Accounts, libraries and settings live on the home. A machine uses them.

### What happens to jobs when a machine goes offline? {#faq-offline}

Its unfinished jobs go back to the queue and another machine or the home takes them.

### How do I update a machine safely? {#faq-update}

You need not: machines update by themselves after the home, one at a time, each drained of its jobs first and checked before the next. To update one now, press **Retry** on its row on [Admin → Updates](/admin/updates). See [Updates](/help/updates).

### Can I stop a machine doing transcription only? {#faq-one-queue}

Yes. Set that queue's switch to **Off** in **Queues this machine pulls from**.

## When something goes wrong {#troubleshooting}

### The home is behind (update the home) {#home-behind}

- **What it means:** the machine shows **paused: home database at X, this machine needs Y — update the home**. The machine runs a newer Maktaba than the home's database.
- **Does Maktaba fix it itself?** It waits. The machine takes no jobs and its web address shows the paused page until the home is updated; then it carries on by itself.
- **What you can do:** update the home. See [Updates](/help/updates).

### This machine is behind (update this machine) {#machine-behind}

- **What it means:** the machine shows **paused: the home's database needs this machine at X or newer (it has Y) — update this machine**.
- **Does Maktaba fix it itself?** It waits. The machine takes no jobs until it runs the needed version.
- **What you can do:** update that machine. See [Updates](/help/updates).

### This machine cannot reach its home {#home-unreachable}

- **What it means:** the machine lost its connection to the home, so its signed-in pages stop.
- **Does Maktaba fix it itself?** Yes. The page checks again every few seconds and reloads when the home answers.
- **What you can do:** check that the home is on and on the network. Meanwhile use Maktaba on the home's address shown on the page.

### Shared storage failing {#storage-failing}

- **What it means:** the machine cannot read or write a share (**Read: failing** or **Write: failing**, or **not mounted here**).
- **Does Maktaba fix it itself?** It skips the queues that need files and checks again; when the check passes it pulls from them again.
- **What you can do:** mount the share on that machine, or fix its row with **Edit mounts**. If the NAS address changed, edit it under **Storage servers**.

### Wrong code {#wrong-code}

- **What it means:** the code typed does not match the one the machine shows. **Wrong code, N tries left.**
- **Does Maktaba fix it itself?** No. After 5 wrong tries the request is closed.
- **What you can do:** read the code again on the machine and retype it. Arabic digits are fine.

### This request has expired {#request-expired}

- **What it means:** the join request was not approved within 15 minutes.
- **Does Maktaba fix it itself?** No.
- **What you can do:** make sure **Accept new machines** is on, then ask to join again from the machine.

### Worker only {#worker-only}

- **What it means:** the machine was installed to pull jobs only; it does not serve the web app or video.
- **Does Maktaba fix it itself?** Yes, when its badge says **becomes a full node at its next update**: it converts by itself in the next update's quiet window.
- **What you can do:** when the badge says **re-run the installer once**, its pipeline is too old to convert by itself: re-run the installer on it once (with the full option to convert at once).

### VOD served by another machine {#vod-fallback}

- **What it means:** **VOD served by …: share not readable here**. This machine cannot read the share, so another machine plays its videos.
- **Does Maktaba fix it itself?** Yes. Playback works through the other machine.
- **What you can do:** fix the machine's share (see [Shared storage failing](/help/machines#storage-failing)).

### Couldn't load the machines {#machines-load-failed}

- **What it means:** the page could not read the cluster from the home. **Couldn't refresh — showing the last known state.** means the last refresh failed but earlier data is still shown.
- **Does Maktaba fix it itself?** Yes. It tries again every few seconds.
- **What you can do:** click **Try again**. If it keeps failing, check [System health](/help/system-health).

### A machine was refused {#machine-refused}

- **What it means:** a machine tried to connect to the live view and the home refused it; the reason follows.
- **Does Maktaba fix it itself?** No.
- **What you can do:** check that the machine is approved and not revoked. A revoked machine must be removed and join again.

### Live view unavailable {#live-unavailable}

- **What it means:** the page cannot connect to the home's live feed, so **Running now** and the live tabs are empty.
- **Does Maktaba fix it itself?** Yes. It reconnects by itself (**Reconnecting…**).
- **What you can do:** wait a moment or reload the page. The machine cards above still refresh.
