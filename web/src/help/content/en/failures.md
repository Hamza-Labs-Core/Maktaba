# Failures

When a processing step cannot finish, Maktaba records why. This page explains every cause you can see in **Needs attention**, on a queue's page under **Failures by cause**, in an item's **History** and on the [Transcription](/transcription) page.

## What it is {#what-it-is}

**Needs attention** on the [Processing](/queue) page lists the open problems. Failures are grouped by step and by cause: 40 files that timed out on the NAS in the same step are one group, not 40 lines. Each group shows how many items it holds, the step, when it was last seen, a sample message, a few of the items (click one to see its history) and one button.

Before an item lands here, Maktaba has already tried it again by itself:

- Most steps try an item up to 3 times. A few try fewer: **Stage TV change** and **Roll back TV change** 2 times, **Swap TV change**, **Repair broken files** and **Canonical share paths** once.
- Between tries it waits about 1 minute, then about 2 minutes. While it waits, the queue shows **Retrying after a pause**.
- Some causes are not worth trying again at once (a YouTube bot check, a step no worker can run, a step that asked for work it may not ask for). Those fail on the first try.
- After the last try the item is **Failed** and its group appears here. It stays until you retry it, or until the item runs again and succeeds (for example after it changes and is queued again).

Two groups are not failures: **Waiting for your review** (changes waiting for **Apply**) and **No worker has synced this stage recently**.

## How to use it {#how-to-use}

1. Open [Processing](/queue) and look at **Needs attention**.
1. Find the cause of a group below, by the label the app shows.
1. Fix what it names (the share, a sign-in, a worker).
1. Press the group's button.

The button depends on the cause:

- **Retry** queues every item of the group again, ahead of other work.
- **Open settings** opens [Extensions](/admin/extensions).
- **Show workers** opens [System health](/admin/health).
- **Apply** writes the changes waiting for review.

On a queue's page, **Retry failed** retries every retryable group of that queue at once. Only an admin can press these buttons.

## What the states and messages mean {#states}

- **Needs attention** (header): at least one group is open.
- **Nothing needs attention.**: no open failures.
- **last seen …**: when the newest failure of the group happened.
- **Failing** (queue tile): the queue has failed items and nothing runs.
- **Gave up** (queue page): how many items of this queue failed for good; below it, how many failures in the last 24 hours.
- **Retry (NAS open p95 …)**: the retry button of a NAS cause also shows how slow the NAS is to open files right now. A high number means the share is still busy; retrying now may fail again.
- **attempt …** (Transcription page): how many times the item was tried.

## FAQ {#faq}

### Does Maktaba retry by itself? {#faq-auto-retry}

Yes, a few times with a pause in between, for every cause that may pass on its own. It stops after the last try (usually the third) and waits for you. It does not keep retrying forever.

### Is Retry safe? {#faq-retry-safe}

Yes. It only queues the failed items again. Work that already succeeded is not redone.

### Why did one group's count drop by itself? {#faq-count-drop}

An item leaves the group when it runs again and succeeds, for example because it was queued again by a later change.

### Why is the label not in Arabic? {#faq-label-language}

The cause labels come from the server and are in English for now. The Arabic page of this guide gives each one an Arabic name and keeps the English label next to it.

## When something goes wrong {#troubleshooting}

### NAS read timed out {#nas-timeout}

- **What it means:** reading the video from the network share took too long. The share was busy, asleep or briefly offline. Any step that reads files can hit it.
- **Does Maktaba fix it itself?** Yes, it retries each item a few times with a pause. If the share stays slow, the items end up here.
- **What you can do:** check that the NAS is awake and reachable and not busy with something else (a backup, a scrub). Look at **NAS reads** and **NAS open time** under **System health**. When it is calm again, press **Retry**. If it happens often, lower the **NAS reads** pool on the queue's page.

### NAS refused the read (Invalid argument) {#nas-invalid-argument}

- **What it means:** the share refused to open or read the file with "Invalid argument". This usually comes from the network share, not from the file itself.
- **Does Maktaba fix it itself?** Yes, it retries a few times.
- **What you can do:** check the share connection on the machine that ran the step, then press **Retry**. If the same files keep failing, open one from another computer to see whether it is readable.

### NAS returned a partial read {#nas-partial-read}

- **What it means:** reading a video's fingerprint got only some of the parts it needs, because the share failed some reads. What was read is kept.
- **Does Maktaba fix it itself?** Yes, the retry reads only what is missing.
- **What you can do:** usually nothing. If the group stays, the share is overloaded: lower the **NAS reads** pool, wait, then press **Retry**.

### File is missing on disk {#file-missing}

- **What it means:** the file is not where Maktaba last saw it. It was moved, renamed or deleted, or the share is not mounted.
- **Does Maktaba fix it itself?** It retries a few times, which helps only when the share was briefly gone.
- **What you can do:** check that the share is mounted. If the file was moved or deleted on purpose, scan the library again so Maktaba learns the new state, then press **Retry** for anything left.

### Could not decode the media file {#ffmpeg-decode}

- **What it means:** the file could not be read as a video, often an unfinished download ("moov atom not found") or a damaged file.
- **Does Maktaba fix it itself?** It retries a few times, which rarely helps. For YouTube downloads, **Repair broken files** can fetch the video again, but it is off until you turn it on.
- **What you can do:** play the file to check it. Replace it yourself, or turn on [Broken files](/help/rules#repair) in [Settings → Rules](/settings/rules) and run that step. Then press **Retry**.

### YouTube asked for a bot check {#youtube-bot-check}

- **What it means:** YouTube asked Maktaba to prove it is not a bot, so it stopped asking. What was already fetched is kept.
- **Does Maktaba fix it itself?** No. It does not retry at once, because that makes the block last longer.
- **What you can do:** wait a few hours. For exact dates and repairs you can let Maktaba use a browser that is signed in to YouTube: **Use a signed-in browser** under [Exact dates](/help/rules#exactdates) or [Broken files](/help/rules#repair) in [Settings → Rules](/settings/rules). Then press **Retry**. The group's **Open settings** button opens Extensions, which has no YouTube setting.

### YouTube video or channel unavailable {#youtube-unavailable}

- **What it means:** YouTube says the video is unavailable or the channel does not exist: it was removed, made private or renamed.
- **Does Maktaba fix it itself?** It retries a few times, which helps only if YouTube had a passing problem.
- **What you can do:** check the link in a browser. If it is really gone, there is nothing to fetch; your local file stays as it is. Press **Retry** only if it is back.

### Plex is unreachable {#plex-unreachable}

- **What it means:** Maktaba could not connect to your Plex server (refused, timed out or the name did not resolve).
- **Does Maktaba fix it itself?** Yes, it retries a few times with a pause; meanwhile the queue shows **Plex not answering, retrying**.
- **What you can do:** check that Plex is running and reachable from the Maktaba machine. Press **Open settings**, check the Plex address and press **Test connection**. Then press **Retry**.

### Plex rejected the token {#plex-auth}

- **What it means:** Plex refused Maktaba's sign-in (the token expired, was revoked, or the account lost access).
- **Does Maktaba fix it itself?** It retries a few times, but with the same token every try fails the same way.
- **What you can do:** press **Open settings**, then **Sign in with Plex** again (or paste a new token) and **Test connection**. Then retry the failed items from the queue's page.

### dizqueTV is unreachable {#dizquetv-unreachable}

- **What it means:** Maktaba could not connect to your dizqueTV server.
- **Does Maktaba fix it itself?** Yes, it retries a few times with a pause; meanwhile the queue shows **dizqueTV not answering, retrying**.
- **What you can do:** check that dizqueTV is running. Press **Open settings**, check its address and press **Test connection**. Then press **Retry**.

### Write did not read back correctly {#readback-mismatch}

- **What it means:** Maktaba wrote a change to Plex or dizqueTV and then read it back to check it, and it was not what was written (for example a channel still there after it was removed).
- **Does Maktaba fix it itself?** Yes, it retries a few times.
- **What you can do:** check the item in Plex or dizqueTV. Someone or something may be changing it at the same time. Then press **Retry**.

### No worker handles this stage {#no-handler}

- **What it means:** the worker that picked up the job does not know this step, usually because it runs an older version or the step's extension is not loaded there.
- **Does Maktaba fix it itself?** No; this fails on the first try.
- **What you can do:** press **Show workers** and check their versions. Update or restart the worker that runs the step (see [Machines](/admin/machines)), then retry the items from the queue's page.

### Stage requested work its graph does not allow {#graph-violation}

- **What it means:** a step asked for work it is not allowed to ask for. This is a fault in Maktaba, not in your library.
- **Does Maktaba fix it itself?** No; this fails on the first try.
- **What you can do:** press **Retry** once, in case it was caused by a worker running a different version. If it comes back, note the step and the sample message and report it.

### Database error {#db-error}

- **What it means:** Maktaba's database refused or broke off a request (a lost connection, a conflict between two jobs).
- **Does Maktaba fix it itself?** Yes, it retries a few times.
- **What you can do:** check [System health](/admin/health). When the database is fine, press **Retry**. If it keeps coming, look at the [Logs](/admin/logs).

### Waiting for your review {#awaiting-review}

- **What it means:** not a failure. The step is set to **Review first** and has prepared changes it has not written yet.
- **Does Maktaba fix it itself?** No; it waits for you.
- **What you can do:** look at the changes (an item's history shows **What this write changed**), then press **Apply**. To stop being asked, set the queue to **Automatic** on its page.

### No worker has synced this stage recently {#stale-graph}

- **What it means:** there is work for this step, nothing runs it, and no worker that knows this step has started in the last day.
- **Does Maktaba fix it itself?** No.
- **What you can do:** press **Show workers**. Start or restart the worker (or the machine) that should run this step.

### Other causes {#other}

- **What it means:** a failure that is not one of the causes above. Its label is the kind of failure, a colon and the message with names and numbers taken out, for example "probe_failed: …". Items with the same message are grouped.
- **Does Maktaba fix it itself?** Usually yes, it retries a few times, unless the step said retrying cannot help.
- **What you can do:** read the sample message and open one of the items. Fix what the message names, then press **Retry**. If you cannot tell what it means, look at the [Logs](/admin/logs).
