# Videos and the player

A video's page, its transcript and chapters, and how to watch it.

## What it is {#what-it-is}

Every video has its own page with tabs for what Maktaba knows about it: the description and related videos, metadata, the transcript, chapters, subtitle files, processing steps and viewing statistics. **Play** opens the player.

## How to use it {#how-to-use}

### The video page {#video-page}

At the top you see the title, the detected language and the video's current state. Below are the tabs:

- **Watch**: the description, **Play**, and a card with what the web knows about it (rating, cast, summary) plus **Related in your library** and **More like this**.
- **Metadata**: suggestions for the title, description and other details, to accept or dismiss. See [Finding metadata](/help/metadata).
- **Transcript**: everything said in the video, line by line. Click a time to play from there.
- **Chapters**: the video's chapters. Click one to play from its start.
- **Files**: the subtitle files, by language and format. Click one to download it.
- **Processing**: each processing step for this video, its state, time left and tries. See [Processing](/help/processing).
- **Statistics**: **Total views**, **Unique viewers**, **Avg completion**, **Avg watch time** and **Completion rate**. Admins also see a **Per-user breakdown**.

### Play a video {#play}

Press **Play** on the **Watch** tab. The player opens with the usual controls (play, volume, time bar, full screen). Above the picture you can set:

- **Playback speed**: from 0.5× to 2×.
- **Subtitle size**: 75 % to 150 %.
- **Picture in picture**: keeps the video in a small window while you use other apps.

Marks on the time bar show where chapters start. **Back** returns to the video's page. Maktaba records how far you watched, for your history and the series progress.

### Turn on subtitles {#subtitles}

When a video has subtitles, turn them on from the player's own menu (the captions or three-dots button), or press `C`. Make them bigger with **Subtitle size**.

### Player keys {#player-keys}

Click the player first, then:

- `Space` or `K`: play or pause
- `J` / `L`: back or forward 10 seconds
- `←` / `→`: back or forward 5 seconds
- `,` / `.`: step back or forward one frame
- `0` to `9`: jump to 0 %, 10 % … 90 % of the video
- `M`: mute
- `F`: full screen
- `C`: subtitles on or off
- `+` / `-`: faster or slower

These keys work with an Arabic keyboard too.

### The on-air title {#on-air-title}

**On-air title:** is the short title the TV guide and the channel strip use. It shows where it came from: **by the rules**, **by the language model** or **set by hand**. Admins can press **Change** to write their own, and **Back to the automatic title** to undo that. A title set by hand always wins.

### Find metadata {#find-metadata}

If Maktaba has no web details for a video yet, the **Watch** tab says **No web metadata yet for this video.** Press **Find metadata** to open the **Metadata** tab and search. See [Finding metadata](/help/metadata).

## What the states and messages mean {#states}

- **ready**: the video is fully processed. Other words in that spot are processing steps still running; see [Processing](/help/processing).
- **Missing**, **Corrupted**, **Failed**, **No audio**, **Replaced**: see [Library](/help/library#states).
- **No transcript available.**: the video has not been transcribed yet, or it has no speech.
- **Nothing here yet.**: the tab has nothing to show yet (no chapters, no subtitle files, no processing steps).
- **No web metadata yet for this video.**: no details have been found on the web yet.
- **not made yet** (next to **On-air title:**): no on-air title has been made for it yet.
- **Same series**, **Shared cast**, **Shared topic**, **Same collection**: why a video is listed under **Related in your library**.
- **Video not found.**: the video does not exist or you cannot see its library.

## FAQ {#faq}

### Why is there no transcript? {#faq-no-transcript}

Transcribing takes time, and long videos wait their turn. Follow it on [Transcription](/help/transcription). A video with no sound never gets one.

### Can I share a link to a moment in a video? {#faq-share-moment}

Yes. Click the time of a transcript line or a chapter, then copy the address from the browser. It opens the player at that moment.

### Does the player remember where I stopped? {#faq-resume}

Maktaba saves how far you watched, for your history and the series progress. The player itself starts from the beginning; use a chapter or the transcript to jump ahead.

### Who can change the on-air title? {#faq-who-edits}

Only admins.

## When something goes wrong {#troubleshooting}

### Video not found. {#video-not-found}

- **What it means:** The link points to a video that does not exist, was removed, or sits in a library you cannot see.
- **Does Maktaba fix it itself?** No.
- **What you can do:** Go back to the [Library](/library) and look for it there. If it should be yours to see, ask an admin about [Library access](/help/library-access).

### Something went wrong. (in the player) {#playback-error}

- **What it means:** The video could not be played: the stream did not start, or the file could not be read.
- **Does Maktaba fix it itself?** When the playback link simply expires during a long video, the player gets a new one and goes on from the same spot. Other errors wait for you.
- **What you can do:** Press **Try again**. If it keeps failing, check the video's **Processing** tab and its state; a **Missing** or **Corrupted** file cannot play.

### The video plays but there are no subtitles {#no-subtitles}

- **What it means:** The video has no subtitle file yet, or they are turned off.
- **Does Maktaba fix it itself?** Subtitles come from the video file itself or are made from the transcript, so some appear only after transcription.
- **What you can do:** Press `C` or use the player's captions menu. Check the **Files** tab to see whether any subtitle file exists.
