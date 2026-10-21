# Library

Browse every video Maktaba has found in your library folders.

## What it is {#what-it-is}

The [Library](/library) page lists the videos of every library you may see, as cards or as a list. Maktaba watches your library folders and picks up new and changed files by itself; each new file then goes through [processing](/help/processing) (reading, transcribing, tagging) before it is fully ready.

Which libraries you see is set by an admin (see [Library access](/help/library-access)).

## How to use it {#how-to-use}

### Open a video {#open-a-video}

Click a card (or a title in the list) to open the video's page. See [Videos and the player](/help/videos).

### Sort {#sort}

Pick **Sort**: **Recently added**, **Title** or **Duration**. The arrow button next to it switches between **Ascending** and **Descending**.

**Duration** sorts the videos already shown on the page. Press **Load more** first if you want it to cover more of them.

### Grid or list {#view}

Under **View**, choose **Grid** for cards with posters, or **List** for a table with **Title**, **Duration**, **Language** and **State**. Maktaba remembers your choice in this browser.

### Filter by language {#filter-language}

When the videos have a detected language, small chips with the language codes appear above the list. Click one to show only that language; click it again to show all. **Clear filters** removes the filter.

### Load more {#load-more}

The page shows 60 videos at a time. Press **Load more** at the bottom for the next ones. The number on the button is how many are shown now.

### Scan a library now {#scan-now}

Admins see **Scan now** on a library's page when it has no videos yet. It looks for new and changed files right away instead of waiting.

1. Press **Scan now**. **Scan started.** shows.
2. A progress bar shows **Waiting for a worker**, then **Looking for new and changed files**, with about how many minutes are left.
3. When it ends you see **Scan finished.** and the list reloads.

You can follow a scan, and every other step, on the [Processing](/queue) page. See [the scan stage](/help/stages#scan).

## What the states and messages mean {#states}

Cards only carry a badge when something needs your eye. A video still being processed has no badge.

- **Missing**: the file is no longer where Maktaba found it. See [File missing](/help/failures#file-missing).
- **Corrupted**: the file cannot be read as a video. See [Video could not be decoded](/help/failures#ffmpeg-decode).
- **Failed**: a processing step gave up on this video. Open the video's **Processing** tab, or see [Failures](/help/failures).
- **No audio**: the video has no sound track, so it cannot be transcribed. Nothing to do unless the file is wrong.
- **Replaced**: the file was replaced by a newer one in the library folder.
- **No videos yet**: this library is empty, or nothing in it has been found yet.
- **No videos match your filters.**: the language filter hides everything. Press **Clear filters**.
- **Library not found.**: the link points to a library that does not exist or that you cannot see.
- **Scan started.** / **Scan finished.**: the scan was queued, then ended.
- **Scan failed**: the scan stopped; the reason follows the message.
- **Could not start the scan**: the server refused to start it; the reason follows the message.

## FAQ {#faq}

### I added files to the folder. Why are they not here yet? {#faq-new-files}

Maktaba waits until a new file stops changing (for example, until a copy ends) before it adds it. Big copies over the network can take a while. If they still do not show, an admin can scan the library, or check the [Processing](/queue) page.

### Why do I not see a library my friend sees? {#faq-missing-library}

Each user sees only the libraries an admin gave them. Ask an admin (see [Library access](/help/library-access)).

### Does Maktaba move or change my files? {#faq-files-safe}

No. Maktaba reads your files; it never moves, renames or edits them.

### Why are some videos shown by file name? {#faq-file-name}

Until a video has a title, Maktaba shows its file name. [Finding metadata](/help/metadata) can give it a proper title.

## When something goes wrong {#troubleshooting}

### Scan failed {#scan-failed}

- **What it means:** The scan could not finish. The message names the reason, often a folder that could not be read.
- **Does Maktaba fix it itself?** The folder watcher keeps running and picks up files once the folder can be read again.
- **What you can do:** Make sure the share or disk is reachable, then press **Scan now** again. If the reason mentions a timeout or an unreachable share, see [NAS read timed out](/help/failures#nas-timeout).

### Could not start the scan {#scan-not-started}

- **What it means:** The server did not queue the scan, for example because you are not an admin or the library no longer exists.
- **Does Maktaba fix it itself?** No.
- **What you can do:** Read the reason after the message. Reload the page and try again, or ask an admin.

### Something went wrong. (the list does not load) {#list-error}

- **What it means:** The page could not read the list of videos from the server.
- **Does Maktaba fix it itself?** No, the page waits for you.
- **What you can do:** Press **Try again**. If it keeps failing, check [System health](/help/system-health) or ask an admin.

### A card has no picture {#no-poster}

- **What it means:** The video has no poster yet, or it could not be read.
- **Does Maktaba fix it itself?** Yes, posters are made during processing and appear once ready.
- **What you can do:** Nothing usually. If it stays blank, check the video's **Processing** tab.
