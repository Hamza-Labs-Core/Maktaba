# Failures

When a processing step cannot finish, Maktaba records why. This page explains every cause you can see in **Needs attention**, on a queue's page under **Failures by cause**, in an item's **History** and on the [Transcription](/transcription) page.

## What it is {#what-it-is}

**Needs attention** on the [Processing](/queue) page lists the open problems. Failures are grouped by step and by cause: 40 files that timed out on the NAS in the same step are one group, not 40 lines. Each group shows how many items it holds, the step, when it was last seen, a sample message, a few of the items (click one to see its history) and one button.

Before an item lands here, Maktaba has already handled it by itself. What it does depends on the kind of problem, its [class](#classes):

- **A passing problem** (a NAS timeout, a partial read, a database error, ffmpeg failing on a thumbnail) is tried again, up to 5 times by default (**Tries of a passing failure**). Between tries it waits about 1 minute, then 2, 4 and 8 minutes. While it waits, the queue shows **Retrying after a pause**.
- **A problem no retry can fix** (a damaged file, no permission to read the file, a video gone from YouTube, a missing file while its share answers) fails at once and needs you.
- **Something the job needs is down** (the language model, a share), **or the machine ran out of disk space or Postgres shared memory**: the job waits and spends no try. Maktaba checks these every 60 seconds (**Check every (seconds)**) and puts the waiting jobs back to work as soon as a check sees it healthy again, or after 24 hours (**Waiting jobs retried after (hours)**) at the latest. Waiting jobs are not in **Needs attention**.
- **Anything else** is tried again within the step's own tries: 3 for most steps, 2 for **Stage TV change** and **Roll back TV change**.
- Steps that run once by design (**Swap TV change**, **Repair broken files**, **Canonical share paths**, **Access check**) keep their one try, even for a passing problem.
- Some failures are not worth trying again (a YouTube bot check, a step no worker can run, a step that asked for work it may not ask for). Those fail on the first try.
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

## Failure classes {#classes}

Every failure gets a class that says what Maktaba does with it. The names in brackets are the ones you can type in **Failure classes** under [Queue health](/help/rules#queue-health), to give a failure a different class. The other settings named here are in the same section.

### Passing problem (transient) {#class-transient}

- **What it means:** a problem that usually goes away by itself: a NAS timeout or partial read, a database error, ffmpeg failing on a thumbnail.
- **What Maktaba does:** tries again with a growing pause, up to **Tries of a passing failure** (5). Then the item is **Failed**.
- **What you can do:** if a group stays, fix what it names (often a busy share), then press **Retry**.

### The language model is down (dependency:model) {#class-dependency-model}

- **What it means:** the language model of a step (for example **Check series tags** or **Check clip tags**) did not answer.
- **What Maktaba does:** the job waits and spends no try. Every **Check every (seconds)** it asks the model; once it answers and has the model you set, the waiting jobs go back to work. If the model answered its last check, the failure is not blamed on it and counts as [unknown](#class-unknown).
- **What you can do:** start the model server, or fix its address or model name in [Tag check](/help/rules#tagcheck).

### A share is offline (dependency:share) {#class-dependency-share}

- **What it means:** a file was not found, and the library folder that holds it does not answer either: the share is not mounted or the NAS is off.
- **What Maktaba does:** the job waits and spends no try. Every **Check every (seconds)** it looks at each library folder; once it lists again, the waiting jobs go back to work.
- **What you can do:** mount the share or wake the NAS. Nothing else is needed.

### Out of disk space (resource:disk) {#class-resource-disk}

- **What it means:** the machine ran out of disk space while working.
- **What Maktaba does:** the job waits and spends no try. The checks look at the temporary and data folders; when they have at least **Free disk needed (GB)** (5) free, the waiting jobs go back to work.
- **What you can do:** free some space. See [No space left on the disk](#disk-full).

### Postgres out of shared memory (resource:shm) {#class-resource-shm}

- **What it means:** the database ran out of shared memory, usually during a large query. The items are fine.
- **What Maktaba does:** the job waits and spends no try. Maktaba cannot measure this memory, so the jobs go back to work **Shared memory wait (minutes)** (10) after the last job ran out of it.
- **What you can do:** usually nothing. See [Postgres ran out of shared memory](#shm-full).

### Retrying cannot help (item_permanent) {#class-item-permanent}

- **What it means:** the problem is in the item: a damaged file, no permission to read it, a video gone from YouTube, or a missing file while its share answers.
- **What Maktaba does:** the item fails at once, without a retry, and its group appears in **Needs attention**.
- **What you can do:** fix the item (replace the file, fix the permission, move it back), then press **Retry**.

### Not known (unknown) {#class-unknown}

- **What it means:** a failure none of the classes above knows.
- **What Maktaba does:** tries again within the step's own tries (3 for most steps), then the item is **Failed**.
- **What you can do:** read the message and see [Other causes](#other). If a failure you know is passing or waits on something, give it its class in **Failure classes**.

## What the states and messages mean {#states}

- **Needs attention** (header): at least one group is open.
- **Nothing needs attention.**: no open failures.
- **last seen …**: when the newest failure of the group happened.
- **Failing** (queue tile): the queue has failed items and nothing runs. Jobs that wait on the model, a share or disk space are not failed, so they do not make a tile **Failing**.
- **Gave up** (queue page): how many items of this queue failed for good; below it, how many failures in the last 24 hours.
- **Retry (NAS open p95 …)**: the retry button of a NAS cause also shows how slow the NAS is to open files right now. A high number means the share is still busy; retrying now may fail again.
- **attempt …** (Transcription page): how many times the item was tried.

## FAQ {#faq}

### Does Maktaba retry by itself? {#faq-auto-retry}

It depends on the [class](#classes). A passing problem is tried up to 5 times (**Tries of a passing failure**). A problem no retry can fix (a damaged file, no permission, a video gone from YouTube, a missing file while the share answers) fails at once and needs you. When the model or a share is down, or the disk or Postgres shared memory is full, the job waits without spending a try and goes back to work when a check sees it healthy again (every **Check every (seconds)**), or after **Waiting jobs retried after (hours)**. Anything else is tried within the step's own tries. It never retries forever.

### Is Retry safe? {#faq-retry-safe}

Yes. It only queues the failed items again. Work that already succeeded is not redone.

### Why did one group's count drop by itself? {#faq-count-drop}

An item leaves the group when it runs again and succeeds, for example because it was queued again by a later change.

### Why is the label not in Arabic? {#faq-label-language}

The cause labels come from the server and are in English for now. The Arabic page of this guide gives each one an Arabic name and keeps the English label next to it.

## When something goes wrong {#troubleshooting}

### NAS read timed out {#nas-timeout}

- **What it means:** reading the video from the network share took too long. The share was busy, asleep or briefly offline. Any step that reads files can hit it.
- **Does Maktaba fix it itself?** Yes, it is a [passing problem](#class-transient): each item is tried up to 5 times with a growing pause. If the share stays slow, the items end up here. **Transcribe** does not retry it: there it fails at once.
- **What you can do:** check that the NAS is awake and reachable and not busy with something else (a backup, a scrub). Look at **NAS reads** and **NAS open time** under **System health**. When it is calm again, press **Retry**. If it happens often, lower the **NAS reads** pool on the queue's page.

### NAS refused the read (Invalid argument) {#nas-invalid-argument}

- **What it means:** the share refused to open or read the file with "Invalid argument". This usually comes from the network share, not from the file itself.
- **Does Maktaba fix it itself?** Yes, it is a [passing problem](#class-transient): tried up to 5 times with a growing pause.
- **What you can do:** check the share connection on the machine that ran the step, then press **Retry**. If the same files keep failing, open one from another computer to see whether it is readable.

### NAS returned a partial read {#nas-partial-read}

- **What it means:** reading a video's fingerprint got only some of the parts it needs, because the share failed some reads. What was read is kept.
- **Does Maktaba fix it itself?** Yes, it is a [passing problem](#class-transient): tried up to 5 times, and each retry reads only what is missing.
- **What you can do:** usually nothing. If the group stays, the share is overloaded: lower the **NAS reads** pool, wait, then press **Retry**.

### File is missing on disk {#file-missing}

- **What it means:** the file is not where Maktaba last saw it. It was moved, renamed or deleted, or the share is not mounted.
- **Does Maktaba fix it itself?** It depends on the share. If the library folder does not answer either, the job [waits for the share](#class-dependency-share) without spending a try and goes back to work when it is back. If the share answers, the item [fails at once](#class-item-permanent). After each full scan of the library, Maktaba looks again: a file that is back is queued again by itself.
- **What you can do:** check that the share is mounted. If the file was moved or renamed, put it back or scan the library again so Maktaba learns the new state, then press **Retry** for anything left.

### Could not decode the media file {#ffmpeg-decode}

- **What it means:** the file could not be read as a video, often an unfinished download ("moov atom not found") or a damaged file.
- **Does Maktaba fix it itself?** No, [retrying cannot help](#class-item-permanent), so it fails at once. For YouTube downloads, **Repair broken files** can fetch the video again, but it is off until you turn it on.
- **What you can do:** play the file to check it. Replace it yourself, or turn on [Broken files](/help/rules#repair) in [Settings → Rules](/settings/rules) and run that step. Then press **Retry**.

### YouTube asked for a bot check {#youtube-bot-check}

- **What it means:** YouTube asked Maktaba to prove it is not a bot, so it stopped asking. What was already fetched is kept.
- **Does Maktaba fix it itself?** No. It does not retry at once, because that makes the block last longer.
- **What you can do:** wait a few hours. For exact dates and repairs you can let Maktaba use a browser that is signed in to YouTube: **Use a signed-in browser** under [Exact dates](/help/rules#exactdates) or [Broken files](/help/rules#repair) in [Settings → Rules](/settings/rules). Then press **Retry**. The group's **Open settings** button opens Extensions, which has no YouTube setting.

### YouTube video or channel unavailable {#youtube-unavailable}

- **What it means:** YouTube says the video is unavailable or the channel does not exist: it was removed, made private or renamed.
- **Does Maktaba fix it itself?** No, [retrying cannot help](#class-item-permanent), so it fails at once.
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
- **Does Maktaba fix it itself?** Yes, it is a [passing problem](#class-transient): tried up to 5 times with a growing pause. A database that ran out of shared memory has its [own entry](#shm-full).
- **What you can do:** check [System health](/admin/health). When the database is fine, press **Retry**. If it keeps coming, look at the [Logs](/admin/logs).

### Postgres ran out of shared memory {#shm-full}

- **What it means:** the database could not get enough shared memory ("could not resize shared memory segment"), usually while large queries run at the same time. The items are fine.
- **Does Maktaba fix it itself?** Yes. The job [waits](#class-resource-shm) without spending a try and goes back to work **Shared memory wait (minutes)** (10) after the last job ran out of it.
- **What you can do:** usually nothing. If it keeps coming, give the database more shared memory (on Docker, a bigger shared memory size for the Postgres container) and check the [Logs](/admin/logs).

### No space left on the disk {#disk-full}

- **What it means:** the machine that ran the step ran out of disk space.
- **Does Maktaba fix it itself?** Partly. The job [waits](#class-resource-disk) without spending a try. When the temporary and data folders have at least **Free disk needed (GB)** (5) free, the waiting jobs go back to work.
- **What you can do:** free some space on that machine (old downloads, temporary files, other programs). [System health](/admin/health) and [Machines](/admin/machines) show each machine's state.

### No permission to read the file {#permission-denied}

- **What it means:** the share refused to let Maktaba read the file ("Permission denied" or "Access is denied"). The file is there, but the account Maktaba uses on the NAS may not read it.
- **Does Maktaba fix it itself?** No, [retrying cannot help](#class-item-permanent), so it fails at once. Maktaba cannot change the share's permissions.
- **What you can do:** give Maktaba's NAS account read access to the file or folder, then press **Retry**. An **Access check** step can test the file from a worker and queue its failed steps again when it reads, but there is no button for it in the app yet: an admin can start it only through Maktaba's API (the `check_access` action).

### ffmpeg failed on the file {#ffmpeg-rc}

- **What it means:** ffmpeg stopped with an error while making a poster, sprites or chapter frames, without saying more. It can be the share or a damaged file.
- **Does Maktaba fix it itself?** Yes, it is a [passing problem](#class-transient): tried up to 5 times with a growing pause.
- **What you can do:** if the same items keep failing, play one of them. A file that does not play is damaged: replace it, or see [Could not decode the media file](#ffmpeg-decode). Then press **Retry**.

### The language model did not answer {#model-unavailable}

- **What it means:** **Check series tags** or **Check clip tags** could not reach its language model. The app shows it as an [other cause](#other) that starts with "model_unavailable". The items are fine.
- **Does Maktaba fix it itself?** Yes. The job [waits for the model](#class-dependency-model) without spending a try. Every **Check every (seconds)** Maktaba asks the model; when it answers, the waiting jobs go back to work. If the model answered its last check, the failure is treated as [not known](#class-unknown) and tried within the step's own tries.
- **What you can do:** start the model server, and check its address and model name in [Tag check](/help/rules#tagcheck).

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
- **Does Maktaba fix it itself?** Usually yes, it is [not known](#class-unknown) and is tried within the step's own tries (3 for most steps), unless the step said retrying cannot help.
- **What you can do:** read the sample message and open one of the items. Fix what the message names, then press **Retry**. If you cannot tell what it means, look at the [Logs](/admin/logs).
