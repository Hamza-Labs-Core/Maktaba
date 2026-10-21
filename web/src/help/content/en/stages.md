# Processing steps

Every queue on the [Processing](/queue) page is one step. This page says what each step does, what it waits for and what comes after it.

## What it is {#what-it-is}

A step works on one kind of item: a video, a library, a season, or the whole catalog (everything at once, used for channels). When a step finishes and its result changed, it passes the item on to the steps after it. When nothing changed, nothing is passed on, so a quiet library costs almost nothing.

The steps are shown in lanes:

- **Sources**: what Maktaba reads from YouTube (and Plex, with that extension).
- **Episodes**: per-video facts read from the files: identity, length, thumbnails, fingerprints.
- **Seasons & libraries**: placing episodes into series and seasons, topics, titles, collections.
- **Channels**: planning and writing the live TV channels.
- **Transcription**: speech to text, subtitles, search, chapters and topics.
- **Media processing**: scanning folders, posters and maintenance steps. This lane is folded when it has no work.
- **Extensions**: steps that belong to Plex or dizqueTV. They are **Off** until that extension is turned on and set up in [Extensions](/admin/extensions).

## How to use it {#how-to-use}

- Click a tile's title on the Processing page to open that step. Its page shows **Where work comes from and goes to**: the steps that feed it and the steps it feeds.
- To run a step again for some items, use **Re-queue a library or season…** on its page, or **Queue work…** on the Processing page.
- To switch a core step off, tick it under **Leave out** in **Start a run** and start the run. Extension steps are switched on and off with their extension.
- Some steps do nothing until a rule turns them on; the entry below says which, with a link to the rule.

## The steps {#steps}

### YouTube listing {#youtube-sync}

Reads the YouTube channel listings that go with your libraries: their videos, playlists and dates (dates are to the month). It runs per library when a library is processed or on a schedule, and feeds **Link**, **Place episodes** and **Exact dates**. Turn it off with **Read YouTube playlists and dates** in **Start a run**.

### Exact dates {#exact-dates}

Asks YouTube for the exact upload day of series episodes whose date is only approximate, one video at a time, slowly. It follows **YouTube listing** and runs **Place episodes** again with the better dates. Off until you turn on [Exact dates](/help/rules#exactdates) (or **Exact upload dates** in **Start a run**).

### Plex index {#plex-index}

Plex extension. Reads the Plex listing of each library once and keeps it, so later Plex steps do not ask Plex item by item. It runs after **Scan folders** and feeds **Plex collection**, **Plex topic and clip collections** and **dizqueTV show**.

### File identity {#content-hash}

Reads each video file once to give it a real content identity, for videos that were added without reading their file (for example brought in from an earlier tool). It feeds **Small thumbnail**, **Transcribe** and **Poster and sprites**. A file that is an exact copy of another one in the library is noted as a duplicate.

### Length {#probe}

Reads how long each video is, and its streams. It runs for each new or changed file found by **Scan folders** and feeds **Thumbnail**, **Link**, **Place episodes** and **Extract audio**. Files it cannot read appear in [Failures](/help/failures#ffmpeg-decode).

### Thumbnail {#thumb-hash}

Makes a small fingerprint of each video's thumbnail. Episodes of one show often share a look, so **Place episodes** uses it as evidence. Runs after **Length**.

### Link {#link}

Matches each video to its YouTube entry: by the downloader's info file, the exact title, a folded Arabic title, or the title plus the length. It runs after **YouTube listing** or **Length** and feeds **Place episodes**. A video whose title was changed too much stays unlinked and is still grouped from its own evidence.

### Fingerprint {#fingerprint}

Reads a short audio and picture fingerprint (intro, end song, a few frames) to tell shows apart. It only runs for the videos **Place episodes** asks for, and reads parts of the file over the share, so a busy NAS can make it fail with [a partial read](/help/failures#nas-partial-read).

### Style sample {#style-sample}

Samples a few short windows of a video to measure how it is made (picture, motion, sound). It runs only for the episodes **Rate style** asks for.

### Small thumbnail {#thumb-small}

Keeps a small copy of each video's thumbnail for the review pages and the app. It runs after **File identity**, **Thumbnail** or **Poster and sprites**, on the main machine, and feeds **Review pages**.

### Place episodes {#detect}

Sorts each library's episodes into series and seasons, using folders, YouTube playlists and dates, thumbnails and fingerprints. It waits a little after changes so they are handled together, asks **Fingerprint** for samples when it needs them, and feeds almost everything after it: tags, style, titles, collections, review pages and the channel plan. Its rules are in [Detection](/help/rules#detect).

### Samples to candidates {#fp-gate}

A hidden helper of **Place episodes**: it checks whether a season's sample episodes share an intro, end song or frames, and only then asks for the fingerprints of the other candidates. It shows as a line on the tile of the step before it, not as its own tile.

### Tag topics {#tag}

Works out the topics, format, audience and occasion of each season, and tags loose videos so they can be used as filler. It reads titles, playlists and descriptions, and the start of the transcript when there is one. It runs after **Place episodes** and **Transcribe** and feeds **Check series tags**, **Check clip tags** and the channel plan. Rules: [Tagging](/help/rules#tag).

### Check series tags {#check-tags}

A language model reads each series' name and a sample of its titles and corrects its topics. Each series is asked once. It runs after **Tag topics** and feeds **Books** and the channel plan. Rules: [Tag check](/help/rules#tagcheck), where you can turn it off.

### Check clip tags {#check-clips}

A language model checks what each short clip used as filler is about, many titles per question, and damps topics it does not keep. It runs per library after **Tag topics** and feeds **Clip groups** and the channel plan. Rules: [Tag check](/help/rules#tagcheck).

### Display titles {#display-title}

Makes a short, clean on-air title for every video and series: it drops the channel's name, hashtags, emojis, dates and phrases every title repeats. It runs after **Place episodes**. Rules: [Display titles](/help/rules#display-titles), where you can turn it off or let a language model tidy what is left.

### Books {#books}

When a lesson series explains a book, looks the book up once and lets its subject correct the season's topics. It runs after **Check series tags** and feeds the channel plan. Rules: [Books](/help/rules#books), where you can turn it off.

### Topic collections {#topic-collections}

Finds curated playlists that cut across shows (a "file" on one subject) and keeps each as its own collection. An episode keeps its season. Runs after **Place episodes** and feeds **Clip groups** and the channel plan.

### Promo links {#promo-links}

Links each promo, trailer or teaser to the show it advertises, so channels can air it near that show. Runs after **Place episodes** and feeds **Clip groups** and the channel plan.

### Clip groups {#clip-groups}

Puts every short, clip and stand-alone video that is in no season into one playable group: by playlist, questions and fatwas, by topic, or a mixed group; linked promos get their own group. Runs after **Check clip tags**, **Topic collections** or **Promo links**. Rules: [Grouping](/help/rules#groups).

### Rate style {#rate-style}

Rates each season as talk, produced, cinematic or still picture with audio, from the samples **Style sample** measures. A style you pinned with **Pin a show's style** wins. Runs after **Place episodes** and feeds the channel plan.

### Review pages {#review-pages}

Writes one review page per library, to check its seasons by eye. It runs after **Place episodes** and **Small thumbnail**, on the main machine, one at a time. Read the pages under **Reports**.

### Plex collection {#plex-collection}

Plex extension. Keeps one Plex collection per season in step with Maktaba's seasons. It writes to Plex, so it offers **Review first**. Runs after **Place episodes** and **Plex index**.

### Plex topic and clip collections {#plex-groups}

Plex extension. Writes the topic collections and clip groups as Plex collections, and removes the ones that are gone. It writes to Plex and offers **Review first**. Runs after **Topic collections**, **Clip groups** and **Plex index**.

### dizqueTV show {#dizquetv-show}

dizqueTV extension. Keeps one dizqueTV show per season in step with the seasons. It writes to dizqueTV and offers **Review first**. Runs after **Place episodes**, **Plex collection** and **Plex index**.

### Plan channels {#plan-channels}

Plans which live TV channels to build, and their time-slot grids, from everything above: seasons, tags, styles, clip groups and promos. It works on the whole catalog, waits a few minutes after changes, keeps a history of plans, and feeds **Write channels** and **dizqueTV channels**. **Preview channels** under **Reports** runs the same plan without saving it. Rules: [Channels](/help/rules#planner).

### Write channels {#write-channels}

Writes the planned channels to Maktaba's own Live TV. It only touches channels it created, and leaves alone a channel someone edited by hand until they hand it back to the plan. Runs after **Plan channels** and feeds **Plex Live TV**.

### Plex Live TV {#plex-livetv}

Plex extension. Keeps Maktaba's channels and their guide in Plex Live TV up to date, after the channels change and when the guide grows. It writes to Plex and offers **Review first**. Rules: [Plex Live TV](/help/rules#plexlive).

### dizqueTV channels {#dizquetv-channels}

dizqueTV extension. Writes the planned channels into dizqueTV, with their logos. It writes to dizqueTV and offers **Review first**. Runs after **Plan channels** and feeds **Channel audit**. Rules: [dizqueTV export](/help/rules#dizquetv).

### Channel audit {#dizquetv-audit}

dizqueTV extension. Checks whether what each channel airs fits the channel, and writes the **Channel audit** report. Runs after **dizqueTV channels**. Rules: [Channel audit](/help/rules#audit).

### Stage TV change {#tv-stage}

Prepares a TV change next to what is on the air, so it can be checked before it goes live. It runs when an admin stages a change in [TV changes](/help/tv-changes); a change queued behind another one waits. Rules: [TV staging](/help/rules#tv-staging).

### Swap TV change {#tv-swap}

Puts an approved TV change on the air, at its time or when an admin presses Swap now. Tried once only.

### TV staging clean-up {#tv-gc}

A daily clean-up of old schedules, retired channels past their keep time, and staged channels of failed or cancelled changes.

### Roll back TV change {#tv-rollback}

Puts the previous TV release back on the air: when an admin rolls back, when a new channel crashes soon after it went live, or when a swap failed.

### Scan folders {#scan}

Looks for new and changed files in a library's folders. It feeds **Length** for each file it finds (and **Plex index** with that extension). It runs when a library is scanned.

### Extract audio {#extract}

Pulls the audio out of a video for transcription. It runs after **Length** and feeds **Transcribe**.

### Transcribe {#transcribe}

Turns speech into text, sentence by sentence with its time, using a Whisper speech model on the machine's GPU. It runs after **Extract audio** or **File identity** and feeds **Subtitle files**, **Search index** and **Tag topics**. Progress for the whole library is on the [Transcription](/help/transcription) page.

### Subtitle files {#subtitle-gen}

Writes the transcript as subtitle files (.srt and .vtt) next to the video, so Plex, VLC and TVs pick them up. It never overwrites a subtitle file Maktaba did not write. It writes to your share, so it offers **Review first**. Runs after **Transcribe**.

### Search index {#index}

Adds the transcript to search, so you can find a video by what is said in it. The old text of a video is removed first. Runs after **Transcribe**, on the main machine, and feeds **Chapters**.

### Chapters {#chapter-infer}

Finds chapters where the topic of a video shifts and gives each a title from its own words. A video that already has its own chapters keeps them. Runs after **Search index** and feeds **Chapter frames** and **Topics**.

### Chapter frames {#chapter-thumbs}

Makes one picture for the start of each chapter, and removes pictures of chapters that are gone. Runs after **Chapters**.

### Topics {#topic-assign}

Tags a video with the nearest topics of its library. A library needs enough transcribed videos before it has topics. Runs after **Chapters** and feeds **Library topics**.

### Library topics {#topic-recluster}

Builds a library's topics from all its transcribed videos and names each after its most typical video. It waits until a batch of videos is done (up to half an hour) and rebuilds the topics whole each time.

### Poster and sprites {#thumbnail}

Makes poster frames and the preview strip used when you scrub a video. Runs after **File identity**, on the main machine, and feeds **Small thumbnail**.

### Repair broken files {#repair-broken}

For videos whose **Length** step failed, reads the file again; a file that is really broken and came from YouTube is downloaded again, checked and swapped in, and the broken one is kept beside it. It writes to your share, runs only when you start it, and is tried once. Off until you turn on [Broken files](/help/rules#repair).

### Canonical share paths {#storage-canonicalize}

The main machine converts the stored paths of your media to share paths every machine understands. It runs by itself when needed or from the [Machines](/admin/machines) page, and is tried once. Rules: [Canonical share paths](/help/rules#storage).

### Categorize {#categorize}

An older step kept for compatibility. Nothing queues it in this version, so it stays idle.

### Find metadata {#enrich}

Makes metadata suggestions for a video when you press Find metadata or Re-enrich: from what Maktaba already knows, and from a web search a language model reads when that is set up. Nothing is changed until you accept a suggestion. Runs on the main machine. Rules: [Finding metadata](/help/rules#enrich).

## What the states and messages mean {#states}

- **Starts here**: no step feeds this one; it is started by a run, a schedule or a button.
- **Last step**: this step feeds no other step.
- **Plex extension** or **dizqueTV extension** badge: the step belongs to that extension.
- **Set up in Extensions**: the extension is not set up; the step cannot run until it is.
- **Served by …**: the machines whose workers run this step.
- A line like "Samples to candidates: 2 running, 5 waiting" on a tile: a hidden helper step that runs with the step on the tile.

For queue states and waiting reasons, see [Processing](/help/processing#states).

## FAQ {#faq}

### Why did a step run but nothing after it? {#faq-nothing-after}

Its result was the same as before (**Nothing changed**), so there was nothing new to pass on.

### Why do Fingerprint and Style sample show so few jobs? {#faq-few-jobs}

They only read the videos other steps ask for: a few samples per season, not every file.

### Which steps write outside Maktaba? {#faq-writes}

**Subtitle files** and **Repair broken files** write to your share; the Plex and dizqueTV steps write to those servers. They offer **Review first** on their page.

### Can I turn off transcription? {#faq-transcription-off}

Yes: tick **Transcribe** under **Leave out** in **Start a run** and start the run. The steps after it then get nothing new.

## When something goes wrong {#troubleshooting}

### A step stays Off {#step-off}

- **What it means:** it is switched off, it belongs to an extension that is off or not set up, or it needs a rule turned on.
- **Does Maktaba fix it itself?** No.
- **What you can do:** read its entry above. Untick it under **Leave out**, or set up the extension in [Extensions](/admin/extensions), or turn on its rule.

### A step fails {#step-fails}

- **What it means:** items of that step failed after Maktaba's retries.
- **Does Maktaba fix it itself?** It retried each item a few times first.
- **What you can do:** read the cause in [Failures](/help/failures), fix it, then press **Retry** in **Needs attention**.
