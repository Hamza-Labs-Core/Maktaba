# Processing

The **Processing** page shows everything Maktaba does to your videos in the background: what runs now, what waits and why, and what needs you. Viewing is open to every signed-in user; only an admin can pause, retry, re-queue or change settings here.

## What it is {#what-it-is}

Maktaba works in small steps. Each step has its own queue: reading a file's length, matching it to YouTube, placing episodes into seasons, transcribing speech, writing channels, and so on. When a step finishes and something changed, it passes the work on to the next steps by itself. Work that has not changed is skipped. For what each step does, see [Processing steps](/help/stages).

The page has these parts, top to bottom:

- **The header**: the overall state (**All caught up**, **Working**, **Needs attention** or **Paused**), the links **Reports** and **Extensions**, and the buttons **Pause all** and **Queue work…**.
- **Start a run**: start processing for all or some libraries in one go.
- **System health**: NAS reads, NAS open time, CPU, network and backlog over the last 4 hours.
- **Workers**: the worker processes that have reported in, online or offline, and **Manage machines**.
- **Library at a glance**: counts of videos, series, seasons, collections and channels.
- **The queues**, in lanes: **Sources**, **Episodes**, **Seasons & libraries**, **Channels**, **Media processing**, **Transcription** and **Extensions**. Each tile shows its state, a progress ring, time left, and why it waits when nothing runs. Click a tile's title to open that queue.
- **Needs attention** and **Activity**: problems grouped by cause, and the live log.
- **Libraries**: per-library progress, with **Re-queue**.
- **Explain a video** and **Pin a show's style**: why a video landed where it is, and a manual override of a season's production style.

## How to use it {#how-to-use}

### Start a run {#start-a-run}

1. Open **Start a run**.
1. Under **Libraries**, pick **All libraries** or **Only the libraries ticked below**.
1. Under **Write to and read from**, tick what Maktaba may touch: **Season collections in Plex**, **dizqueTV shows and TV channels**, **Read YouTube playlists and dates**, and **Exact upload dates** (slow: one request per video). An extension that is not set up shows **Not set up**; set it up in [Extensions](/admin/extensions) first.
1. Under **Steps**, choose **Start at**. Leave **the first step** to run each library from its first queues, skipping what has not changed. Pick a step to redo that step and everything after it.
1. Under **Leave out**, tick steps you do not want to run.
1. Read the summary line and press **Start run**.

The write switches and **Leave out** are saved settings: they stay as you set them after the run.

### Queue work for some items {#queue-work}

Press **Queue work…** (or **Re-queue** on a library, or **Re-queue a library or season…** on a queue's page). Pick what to queue, which queues (leave all unticked to start each item at its first queue), and **How**:

- **Queue**: add it to the queue; work that has not changed is skipped.
- **Redo**: run the chosen queues again, then pass on only what changed.
- **From the start**: redo it and everything after it.

The result says how many jobs were queued, how many joined jobs already waiting, and how many were skipped.

### Pause and resume {#pause}

- **Pause all** stops anything new from starting anywhere. Running jobs finish. Press **Resume all** to go on.
- On a queue's page, **Pause this queue** stops that queue only. Tick **Also ask running jobs to pause** to ask the jobs running now to stop as well (some steps can only stop when their job ends). **Resume this queue** undoes it.

### Retry failures {#retry}

In **Needs attention**, each group has one button. **Retry** (with the number of items) queues every failed item of that group again, ahead of other work. For NAS causes the button also shows the NAS open time, so you can see whether the share is still slow. On a queue's page, **Retry failed** retries every retryable group of that queue at once. See [Failures](/help/failures) for what each group means. To fix single items (retry, ignore, fix a path, download again and more), use [Failing items](/help/failures#failing-items).

### Open a queue {#queue-page}

A queue's page shows its description, state, the **Waiting**, **Running**, **Rate (10 min)**, **Time left**, **Gave up** and **Median per job** figures, **Where work comes from and goes to**, **Running now**, **Next up**, **Failures by cause** and **This queue's log**.

### Workers per pool {#pools}

Queues that use the same resource share a pool of workers (for example **NAS reads**, **CPU**, **GPU**, **Plex**, **YouTube**). On a queue's page, use **Fewer workers** and **More workers** to change the pool's size, and **Auto** to go back to the default. The note under it says whether the limit is shared by every worker process or applies per worker process, and which other queues share the pool. A change affects every queue in that pool.

### Review before writing {#review-mode}

Queues that write to other systems (Plex, dizqueTV, subtitle files next to your videos, repaired files) show **Writes to other systems** on their page:

- **Automatic**: the change is written as soon as it is ready.
- **Review first**: the change is prepared but not written. The queue shows **Waiting for your review**, and **Needs attention** shows a group with **Apply**. Press **Apply** to write every waiting change of that queue. A plan that changed since it was prepared is held for review again.

### See one item's history {#item-history}

Click an item's name anywhere (in Needs attention, Next up, the log) to open its page. It shows its kinds, **Open in the library**, **Review page** for a library, **Re-queue** per queue with its last result, **Everything from the start**, **Choose queues…**, its **Seasons** (or **Years**), **Where it appears**, and its **History**. Filter the history with **Everything**, **Changes only** or **Problems only**. Each entry may say **Because:** (what triggered it) and **Re-queued:** (what it passed on).

### Reports {#reports}

**Reports** lists the pages the pipeline writes: **Channel plan**, **Plan preview**, **Channel audit**, **Library review pages** and **Other reports**. Pick one to read it on the page, or **Open in a new tab**. An admin can press **Preview channels** to plan the channels with the saved settings without writing anything, and **Show the stored plan** to see the last real plan.

### Keep libraries offline, in MP4 and without duplicates {#offline}

Each row of **Libraries** on this page has a menu, in the order a video goes through: **Refresh from YouTube** (list the library's channels and playlists and download what is new), **Convert to MP4** and **Find duplicates**, plus **YouTube sources…** (see [YouTube sources](#youtube-sources)). **With selected** runs the first three for the ticked rows. The **YouTube**, **Not MP4** and **Duplicates** columns say where each library stands, and **Where each item is** shows each video's download, conversion and duplicate check, with the job's own **Cancel** and **Retry**.

How the work starts:

- **Convert to MP4** from a library's menu plans every video of the library and queues a conversion for each file that needs one. The dialog opens with **Dry run: only write the plan to the job, touch no file** ticked: press **Queue the dry run** to see the plan first (it appears on the job when it finishes), then open the menu again, untick it and press **Queue it** to convert.
- **Find duplicates** from a library's menu checks every converted video of the library for duplicates; a video that is not converted yet is converted first. It also starts with the dry run ticked.
- To convert one video, click its name on this page to open its page, press **Choose queues…**, tick **Convert to MP4** and queue it. There is no separate convert button on the video itself.
- A video downloaded from YouTube is handed on by itself: it is converted as soon as it lands, then checked for duplicates, while the next video downloads.
- Automatic conversion after every scan is off by default (the library setting `convert.auto`), and no scan queues a conversion in this version: start it from the menu.

### How a conversion decides {#convert-decides}

**Convert to MP4** reads each file and picks one of four outcomes:

- **Skip**: the file is already an `.mp4` or `.m4v` with H.264 or HEVC video, AAC, AC-3, E-AC-3 or MP3 audio, and the index at the front of the file, so it plays at once. It is left alone.
- **Remux** (lossless): the video and audio are already fine for MP4 but sit in another container (`.mkv`, `.mov`, `.ts` and so on), or an MP4 has its index at the end. The streams are copied into a new MP4 without re-encoding.
- **Audio only**: the video is fine but the audio is not (Opus, Vorbis or FLAC, the usual YouTube `.webm` case). The video is copied and the audio becomes AAC at 192 kbit/s.
- **Re-encode** (lossy): any other video (VP9, AV1, MPEG-4 Part 2, WMV and so on) becomes H.264. This is **off by default**: a library re-encodes only when its advanced settings say `"convert": {"allow_reencode": true}` (see [Change a library](/help/library-management#edit)). Otherwise the file is skipped as one that needs re-encoding. Re-encoding uses the VideoToolbox H.264 preset taken from FileUtilities (`h264_videotoolbox` at 8 Mbit/s) when the main machine's ffmpeg has it, and `libx264` with CRF 20 (preset `medium`) when it does not or when VideoToolbox fails.

A file with image subtitles (PGS or VobSub, as on Blu-ray and DVD rips) is skipped, because MP4 cannot hold them and they would be lost. Text subtitles are kept inside the MP4 and also written beside it as `<name>.<language>.srt`. Chapters and the file's metadata are kept. A file with no video stream is skipped too.

Nothing is lost on the way. The new MP4 is made in a work folder on the main machine (`~/.maktaba/work/convert`), one file at a time, and is then checked: it is decoded in full, and its length, streams and frame count are compared with the original's. Only a file that passes takes the original's place on the share, where its size is checked again. The original goes to the library's trash (`.maktaba/trash`) for 30 days and can be restored from [File actions](#file-actions) until then. A file that fails the check leaves the original untouched; it is tried once more (re-encoded, where the library allows it) and then shows in [Failures](/help/failures). The video keeps its place, watch history, playlists, transcripts and chapters: nothing is transcribed again.

### YouTube sources {#youtube-sources}

A library's YouTube channels and playlists are its **sources**. When this version was installed, every YouTube channel a library already had became a source set to **Off**, so **nothing downloads until you turn a source on**. To turn one on:

1. On this page, open the menu of the library's row in **Libraries** and choose **YouTube sources…** (the same dialog opens from **YouTube sources…** on [Manage libraries](/help/library-management#youtube-sources)).
1. Turn on the **Downloads** switch of the source. It comes back in the mode it had when it was last on, or **New uploads and back catalogue** the first time. Turning the switch off stops it: nothing more downloads, and what was downloaded stays.
1. While a source is on, **Mode** next to the switch chooses **New uploads and back catalogue** (new uploads download first, then the older videos, a few at a time when YouTube is free) or **New uploads only** (only what is uploaded from now on).
1. Turning a source on queues the library's refresh at once.

A library with one source also has its switch on its card in [Manage libraries](/help/library-management#youtube-sources).

To follow a new channel or playlist, paste its link in **Channel or playlist URL** under **Add a source**, choose its **Mode** (a new source starts as **New uploads and back catalogue**) and its **Folder** inside the library (empty: a folder named after its title), then press **Add**.

**Options…** on a source sets how it downloads; press **Save** to keep them:

- **Highest quality**: the tallest picture to download, from 360p to 2160p (1080p unless you change it).
- **Subtitle languages**: the subtitles to fetch, as language codes (`ar,en` unless you change it).
- **Also automatic subtitles**: also fetch YouTube's generated subtitles (on unless you change it).
- **Audio only**: download the sound without the picture.
- **Cookies from browser**: the browser whose YouTube sign-in to use, for videos that need one.
- **Limit rate**: a download speed limit, such as `2M`.

Each source also has **Retry gone/skipped** and **Remove** (it stops following the source; what was downloaded stays). Under **Library actions**, **Resume back catalogue** carries on a paused back catalogue, **Retry gone/skipped/failed** wants those downloads again, and **Import from Tartube…** marks every video an existing Tartube database downloaded as already downloaded, so it is never fetched twice. To make the sources themselves from Tartube, for every library at once, use **Import from Tartube…** on [Manage libraries](/help/library-management#tartube).

### Duplicates {#duplicates}

**Duplicates** lists the groups of files that are the same video, with each file's path, size, resolution, codec and who watched it. Choose the file to keep and press **Apply**: the kept file takes the others' watch history, playlists, favourites and transcripts, and the other files go to the library's trash for 30 days. **Dismiss** a group that is not a duplicate; it does not come back. Identical files in one library merge by themselves; **Apply all identical** approves those still open.

### File actions {#file-actions}

**File actions** is the record of every file Maktaba converted, merged, downloaded, restored or purged, with what its check measured when it failed. A replaced file waits in its library's trash (`.maktaba/trash`) until its purge date, and **Restore** puts it back until then. The trash of each library, its size and its next purge are at the top; the trash is emptied once a day of what is past its date.

## What the states and messages mean {#states}

### Queue states {#queue-states}

- **Running**: at least one job of this queue is running now.
- **Failing**: many recent jobs of this queue fail the same new way. The tile says how many and the error (**… failing: …**). A few failed items do not turn a tile red. See [… failing: …](/help/failures#tile-failing).
- **Waiting**: there is work, but it cannot start yet. The tile says why (see below). Jobs that wait for the model, a share, disk space or database memory show **… waiting for …, resume by themselves**; see [that note](/help/failures#tile-waiting).
- **Done**: nothing is waiting.
- **Off**: the queue is switched off or not set up.

Below the numbers, a tile can also show two small links: **… failed** (failed items of this queue) and **… need you** (items Maktaba will not try again by itself; see [… need you](/help/failures#needs-you)). Both open [Failing items](/help/failures#failing-items) for that queue, where an admin can fix items one by one.

### Overall state {#overall-state}

- **All caught up**: nothing runs, nothing needs you.
- **Working**: jobs are running.
- **Needs attention**: some groups in Needs attention wait for you.
- **Paused**: everything is paused with **Pause all**.

### Job states {#job-states}

- **Waiting**: queued, not started.
- **Starting**: a worker has picked it up.
- **Running**: in progress.
- **Paused**: stopped for now; it continues later. A job whose worker stopped answering is also paused and picked up again.
- **Resuming**: continuing after a pause.
- **Done**: finished.
- **Failed**: gave up after its tries.
- **Cancelled**: stopped on purpose.

### Results {#results}

- **Changed something**: the step produced something new; the next steps run.
- **Nothing changed**: the step ran and the result was the same; nothing is passed on.
- **Skipped, nothing new**: the inputs had not changed, so it did not run.
- **Waiting for review**: a change is ready and waits for **Apply**.
- **Waiting for inputs**: the step asked for other work first (for example samples to read) and runs again when that is done.
- **Imported**: brought in from an earlier tool.
- **Replaced by a newer run**: a newer job for the same item took its place.

### Why a queue waits {#waiting}

When nothing runs in a queue, its tile says why. Maktaba checks the reasons in this order and shows the first that applies.

### Switched off {#waiting-disabled}

The queue is off: you left it out in **Start a run**, or its extension is switched off. Nothing runs. Turn it back on by unticking it under **Leave out** and starting a run, or turn the extension on in [Extensions](/admin/extensions).

### Not set up {#waiting-not-configured}

The queue belongs to an extension (Plex or dizqueTV) that is not set up yet. Open [Extensions](/admin/extensions), fill in its settings and press **Test connection**.

### Its component is off {#waiting-component-off}

The queue belongs to a component (for example Transcription) that an admin switched off. No new jobs are made while it is off, and the jobs already queued stay queued. When the component is on again, Maktaba queues the work it missed and the queue carries on.

### Its component is stopped {#waiting-component-held}

An admin stopped the component this queue belongs to for a while. New jobs still queue and wait; none starts until the component is started again.

### Everything is paused {#waiting-paused-all}

Someone pressed **Pause all**. Nothing new starts in any queue. Press **Resume all**.

### Paused {#waiting-paused}

This queue alone is paused. Open it and press **Resume this queue**.

### Waiting for your review {#waiting-awaiting-review}

The queue is set to **Review first** and has changes ready. Press **Apply** in Needs attention, or set the queue to **Automatic**.

### Waiting for a worker that serves this step {#waiting-no-worker}

Work is waiting, but no online worker runs this queue. Check that the worker (or the machine that should run it) is on: see **Workers** on this page, [System health](/admin/health) and [Machines](/admin/machines).

### Waiting for a free worker {#waiting-pool-full}

Every worker of this queue's pool is busy, often with another queue that shares the pool. The work starts when a slot frees up. To go faster, raise the pool size on the queue's page.

### Waiting for changes to settle {#waiting-debouncing}

Some steps wait a short while after a change so that many changes are handled in one go (for example the YouTube listing, placing episodes, planning channels, building library topics). It starts by itself.

### Retrying after a pause {#waiting-backoff}

A job failed and Maktaba waits before trying again: about 1 minute after the first failure, 2 after the second, longer after that, never more than about an hour. It retries by itself.

### Plex or dizqueTV not answering, retrying {#waiting-service-retrying}

Plex or dizqueTV did not answer, so jobs of this queue wait and try again later by themselves. If it lasts, check that the server is running and reachable, and use **Test connection** in [Extensions](/admin/extensions).

### Waiting for earlier steps {#waiting-upstream}

Nothing is queued here yet, but steps before this one are still running and will pass work on. Nothing to do.

### Nothing waiting {#waiting-idle}

The queue has nothing to do. It runs again when something changes.

### Other messages {#messages}

- **Live updates are paused; refreshing every few seconds instead.**: the live connection dropped; the page still refreshes.
- **Work is waiting but nothing has run for over an hour. Is a worker running?**: check the workers.
- **No activity for … It may be stuck or stopped; check the workers and the log.**: nothing has happened for a long time while work waits. Check the workers and the log.
- **… no heartbeat for …**: a running job stopped reporting. If its worker is gone, the job is paused and picked up again.

## FAQ {#faq}

### Why does a queue say Done when my library is not finished? {#faq-done}

Done means that queue has nothing waiting. Later steps may still be busy, or earlier ones have not passed work on yet. Look at the whole flow, or open the library's row in **Libraries**.

### Does a new run redo everything? {#faq-redo}

No. Every step is saved per item, so a run only does what is left or what changed. Use **Redo** or **From the start** when you want work done again.

### Can I make transcription faster? {#faq-faster}

Raise the size of the pool the queue uses on its page, or add a machine that serves it. Shared pools slow other queues down, so raise them a little at a time.

### What is the difference between Pause all and pausing one queue? {#faq-pause}

**Pause all** holds every queue. Pausing one queue holds only that one; the steps before it keep working and their results wait for it.

### Who can change things here? {#faq-admin}

Everyone signed in can look. Only an admin can pause, retry, apply, re-queue, start a run or change pools.

## When something goes wrong {#troubleshooting}

### The queues could not be loaded. {#load-error}

- **What it means:** the page could not reach the server.
- **Does Maktaba fix it itself?** No; the page does not retry on its own.
- **What you can do:** press **Try again**. If it keeps failing, check that the Maktaba server is running.

### A group in Needs attention {#needs-attention}

- **What it means:** items failed for the same cause after Maktaba's own retries.
- **Does Maktaba fix it itself?** It already retried each item a few times before it gave up.
- **What you can do:** read the cause in [Failures](/help/failures), fix what it names, then press the group's button.

### Nothing moves at all {#nothing-moves}

- **What it means:** usually everything is paused, no worker is running, or the work waits for review.
- **Does Maktaba fix it itself?** No.
- **What you can do:** check the header for **Paused**, the **Workers** strip, and each tile's waiting reason above.
