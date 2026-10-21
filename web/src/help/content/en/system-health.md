# System health

See at a glance whether Maktaba is working: its services, its processes, free disk space, the processing backlog, Live TV and the version you run. Only administrators see this page.

## What it is {#what-it-is}

[Administration → System health](/admin/health) collects the health of every part of Maktaba on one page. It refreshes by itself every 30 seconds (**Auto-refreshes every 30s**).

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
2. Look at **Overall status**. **Operational** means everything answers.
3. If it says **Degraded** or **Down**, scroll to **Services** and find the card with the same badge. Its reason and its failing check say what is wrong.
4. Check **Processes** for a process that is **Restarting**, **Failed** or in a crash loop.

### Find the version you run {#find-the-version}

The **Version** card shows the version number and build. Give these when you report a problem. To see each machine's version, open [Machines](/help/machines).

### Read the details behind a problem {#read-details}

The page shows states, not logs. To see why something fails, open [Logs](/help/logs) and filter by **Error**.

## What the states and messages mean {#states}

### Services {#service-states}

- **Operational**: the service answers and its checks pass.
- **Degraded**: it answers but a check fails, or its state is unknown. It may work partly.
- **Down**: it does not answer, or a check failed hard. When the whole page cannot reach the server, **Overall status** shows **Down**.

### Processes {#process-states}

- **Running**: up and answering.
- **Starting**: just started; it is coming up.
- **Not responding**: running but its health check fails (**failing (N in a row)**). Maktaba restarts it if it stays that way.
- **Restarting**: it stopped and Maktaba waits a moment before starting it again (**Next restart**).
- **Failed**: it could not be kept running.
- **Stopped**: it was stopped on purpose.
- **Crash loop**: it keeps exiting soon after it starts. The other processes keep running, and this one is retried slowly.
- **Recent crashes** lists why each stop happened: **exited with code N**, **killed by …**, **stopped responding and was restarted**, or **could not start**.

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
