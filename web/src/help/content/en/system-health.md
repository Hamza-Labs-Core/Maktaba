# System health

See at a glance whether Maktaba is working, and if not, what is wrong and what to do: its services, workers, dependencies, Plex, queues and Live TV, its processes, free disk space and the version you run. Only administrators see this page.

## What it is {#what-it-is}

[Administration → System health](/admin/health) collects the health of every part of Maktaba on one page. It refreshes by itself every 30 seconds (**Auto-refreshes every 30s**).

- **The banner** at the top: **All systems OK**, **Degraded** or **Down**, with the reason. It is Down only when a core part is down: the API, the database, every worker, or streaming while channels are on air. Anything else that is not OK makes it Degraded. When it is Down, **System health** in the menu shows a red dot.
- **Services**, **Workers**, **Dependencies**, **Integrations** and **Queues & TV**: one tile per part, green (OK), yellow (Degraded) or red (Down), with a one-line reason, when it was last checked and **Learn more**, which opens its entry below. See [Each part](#items).
- **Overall status**: one word for the whole server.
- **Disk free**: free space on the server's data disk.
- **Queue depth**: how many jobs wait to be processed.
- **Transcribe budget left**: shown only when a paid transcription budget is set.
- **TV**: machines online, channels on air, failovers in the last 24 hours, and whether remote viewing is ready.
- **Services**: one card per service, each with its own status and checks.
- **Processes**: the API, **Live TV and streaming** and the **Processing pipeline** processes, how long they have run, how often they restarted, and **Recent crashes**.
- **Caches**, **Performance budgets** and **Version** (version, build, build date and the database **Schema revision**).

## How to use it {#how-to-use}

### See what is down {#see-what-is-down}

1. Open [Administration → System health](/admin/health).
2. Read the banner. **All systems OK** means every part answers and is fine.
3. If it says **Degraded** or **Down**, it names the parts that are not OK. Find their tiles: yellow or red, each with its reason.
4. Press **Learn more** on a tile to read what that part is and what you can do.
5. Check **Processes** for a process that is **Restarting**, **Failed** or in a crash loop.

### Workers kept as history {#worker-history}

A worker that restarted gets a new name; its old record stays behind. When a newer worker runs on the same machine, or the old one has been silent longer than **Forget a worker after (hours)**, the old record is listed under **History** in grey. It is never counted as a failure.

### Change when a part turns yellow or red {#change-thresholds}

The limits are rules: open [Settings → Rules → System health](/help/rules#system-health). **Heartbeat late after** and **Heartbeat gone after** decide when a worker or streaming machine is late or gone; **Database slow from** when the database is slow; **Dependency check stale after** when a dependency check is too old; **One check waits** how long the page waits for each part.

### Find the version you run {#find-the-version}

The **Version** card shows the version number and build. Give these when you report a problem. To see each machine's version, open [Machines](/help/machines).

### Read the details behind a problem {#read-details}

The page shows states, not logs. To see why something fails, open [Logs](/help/logs) and filter by **Error**.

## What the states and messages mean {#states}

### Services {#service-states}

- **Operational**: the service answers and its checks pass.
- **Degraded**: it answers but a check fails, or its state is unknown. It may work partly.
- **Down**: it does not answer, or a check failed hard. When the whole page cannot reach the server, **Overall status** shows **Down**.
- **Did not answer in time**: the part did not answer within **One check waits** (2 seconds). It is shown yellow, or red for the database.

### Processes {#process-states}

- **Running**: up and answering.
- **Starting**: just started; it is coming up.
- **Not responding**: running but its health check fails (**failing (N in a row)**). Maktaba restarts it if it stays that way.
- **Restarting**: it stopped and Maktaba waits a moment before starting it again (**Next restart**).
- **Failed**: it could not be kept running.
- **Stopped**: it was stopped on purpose.
- **Crash loop**: it keeps exiting soon after it starts. The other processes keep running, and this one is retried slowly.
- **Recent crashes** lists why each stop happened: **exited with code N**, **killed by …**, **stopped responding and was restarted**, or **could not start**.

### Each part {#items}

Each tile is one part of Maktaba. What each one checks, and what to do when it is not OK:

### API {#item-api}

The server this page talks to, with its version and how long it has run. If you can read the page, it is up.

### Database {#item-postgres}

Maktaba's Postgres database: does it answer, and how fast (**Database slow from**, 250 ms). It also says whether this server is the primary and how many replicas stream from it, when Postgres lets Maktaba see it. **Down** makes the whole page Down: nothing works without the database. Check that Postgres runs and that the disk is not full.

### Streaming {#item-streaming}

The streaming service that plays videos and airs the channels: does it answer at its address, how many channels are on air and how many encoder sessions run. Down while channels are on air makes the whole page Down: the channels are off the air. On the Mac, **Pause server** then **Resume server** restarts it.

### Pipeline {#item-pipeline}

The processing pipeline's search port, used for searching by meaning. When it does not answer, search still finds words. **Not configured** means this server was started without its address.

### Process {#item-process}

A process maktaba-server runs (the API, streaming, the pipeline): **Running**, restarting, failed or in a crash loop. See [Processes](#process-states).

### Workers {#item-workers}

All workers together: how many are online. **Down** when no worker runs: nothing is processed. Start the pipeline (or the Mac app) on at least one machine.

### Worker {#item-worker}

One worker on one machine: its role (**main** or **transcription**), when it last reported, and what it is running now. **Late** after **Heartbeat late after** (60 s), **Gone** after **Heartbeat gone after** (300 s). A gone worker's machine may be asleep, off or disconnected. See [Workers kept as history](#worker-history).

### Streaming machine {#item-streaming-host}

A machine that airs channels: online, draining, switched off, unable to take channels, or without a heartbeat. See [Machines](/help/machines).

### Dependency {#item-dependency}

Something jobs need that Maktaba checks every minute (Queue health): the language model at the Tag check address, each library share, the free disk space and the database's shared memory. **Down** while it is not available: jobs that need it wait instead of failing and start again by themselves when it is back. **Stale** when no check ran for a while: the worker that runs the checks may be stopped. See [Failure classes](/help/failures#classes).

### Plex Live TV {#item-plex-livetv}

Maktaba's tuner in Plex: Plex answers, the tuner is registered, the channels are in sync, and the last error. See [Plex](/help/plex). On the Mac app, Plex on your network that does not answer with "no route to host" usually means macOS keeps Maktaba off the Local Network: see [Local Network](/help/mac-app#local-network).

### Folder access {#item-host-access}

Whether the server can read each library folder, tried every time the page refreshes. Shown when maktaba-server runs the server (the Mac, Windows and Linux apps), not for a Docker install. One tile for all the folders, and one per folder:

- **Can be read**: fine.
- **macOS does not let Maktaba read this folder**: Maktaba has no Full Disk Access, or the server started before you turned it on. Turn it on in System Settings → Privacy & Security → Full Disk Access (click **+** and choose Maktaba in Applications if it is not listed), then choose **Pause server** and **Resume server** in the menu bar. See [Permissions](/help/mac-app#permissions).
- **No permission to read this folder** (Windows, Linux): the account the server runs as cannot read it. Give that account read access to the folder.
- **The volume is not mounted** / **The share is not mounted**: the NAS share is not connected on this machine. Connect it (on a Mac it appears in Finder under Locations), then wait for the next check.
- **The folder does not exist**: it was moved, renamed or deleted. Put it back, or change the library to its new place.
- **Did not answer**: the read is still waiting, often a NAS that is asleep or disconnected. Maktaba keeps one read waiting per folder and shows its answer when it comes.

### Queues {#item-queues}

The processing queues' health: how many are failing, how many wait on a dependency and how many items need you. **Learn more** on a failing queue: [Queue tile turns Failing](/help/failures#tile-failing); the items: [Failing items](/queue/failures).

### Channels on air {#item-tv-channels}

How many channels are on air, and any that stopped reporting. See [Live TV](/help/live-tv-admin).

### TV change {#item-tv-change}

The TV change that is open. **Waits for your approval** when it was checked and is ready: open it and approve it. See [TV changes](/help/tv-changes).

### Live TV slot {#item-tv-slot}

The slot that airs the channels now (staging, Epic 33): healthy while it reports every few seconds. **The TV runs in one process** when slots are off.

### TV tuner {#item-tv-tuner}

The tuner Plex and other apps use: on or off, and how many of its tuners are in use. **Every tuner is in use**: one more viewer is refused; raise the tuner count on the [Live TV](/help/live-tv-admin) page.

### TV line {#tv-line}

- **remote viewing ready** / **off** / **not set up** / **has a problem**: the state of watching Live TV away from home. See [Remote TV](/help/remote-tv).

## FAQ {#faq}

### Can I restart a process from this page? {#faq-restart}

No. Maktaba restarts a stopped or stuck process by itself. The page shows how often it did and why. On the Mac app use **Pause server** and **Resume server** in the menu bar; see [The Mac app](/help/mac-app).

### Why is the Processes card empty? {#faq-processes-unavailable}

It shows _Process supervision is not available_ when Maktaba is not run by its own server launcher (for example in a container). The other cards still work.

### What is Queue depth? {#faq-queue-depth}

The jobs waiting in the processing queues. A large number while the library is being scanned is normal. See [Processing](/help/processing).

## When something goes wrong {#troubleshooting}

### Overall status Down {#overall-down}

- **What it means:** the page could not reach the server's health check, or a core service is down.
- **Does Maktaba fix it itself?** It restarts crashed processes by itself and the page checks again every 30 seconds.
- **What you can do:** wait a minute. If it stays down, open [Logs](/help/logs) and look at the errors, or restart the server (on the Mac, **Pause server** then **Resume server**).

### A service is Degraded {#service-degraded}

- **What it means:** the service answers but one of its checks fails, for example a connection it needs.
- **Does Maktaba fix it itself?** Usually yes, once the thing it waits for answers again.
- **What you can do:** read the reason on the card. For Plex, dizqueTV or storage problems see [Failures](/help/failures).

### Crash loop {#crash-loop}

- **What it means:** a process keeps exiting soon after it starts.
- **Does Maktaba fix it itself?** It keeps retrying, more slowly each time. The rest of Maktaba keeps running.
- **What you can do:** read **Last crash** and the process's errors in [Logs](/help/logs). A recent update or a full disk are common causes; check **Disk free**.

### Could not load this section {#section-error}

- **What it means:** one card could not get its data. The rest of the page is fine.
- **Does Maktaba fix it itself?** Yes. The page tries again every 30 seconds.
- **What you can do:** wait for the next refresh. If one card always fails, report it with the version shown on the page.

### Disk free is low {#disk-low}

- **What it means:** the server's data disk is almost full. Processing, transcription and the database need room.
- **Does Maktaba fix it itself?** No.
- **What you can do:** free space on that disk, or move large files off it.
