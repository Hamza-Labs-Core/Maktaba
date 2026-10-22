# Transcription

The **Transcription** page shows how far Maktaba is with turning the speech of your whole library into text: how much is done, how fast it goes, how long is left, and what failed.

## What it is {#what-it-is}

Each video goes through three steps: **Transcribe** (speech to text), **Subtitles** (subtitle files next to the video) and **Search index** (the text becomes searchable). This page adds them up for every library. The work itself runs in the processing queues; this page only reports on it, so it has no buttons that change anything. For the steps themselves, see [Transcribe](/help/stages#transcribe), [Subtitle files](/help/stages#subtitle-gen) and [Search index](/help/stages#index).

The page updates live while jobs finish.

## How to use it {#how-to-use}

Read the page from top to bottom:

- **Audio hours transcribed**: the share of all audio that has text, as a percentage and in hours, with the hours **left** below.
- **Videos transcribed**: the share of videos that have text, and how many subtitle segments exist.
- **Speed**: how fast the work goes, as a multiple of real time. **Decoding now** shows the speed of the workers at this moment and how many are working. **Last 10 minutes** and **Last hour** show how much audio from how many videos was done, and the average speed per video.
- **Time left**: the time until everything is transcribed, the hour it should be done, and which speed it is based on.
- **Stages**: for **Transcribe**, **Subtitles** and **Search index**, how many videos are **Waiting**, **Running**, **Done** and **Failed**. Click a stage's name to open its queue.
- **Libraries**: per library, videos and hours done out of the total, with a progress bar. Sort it with **Sort by**, or by clicking a column heading.
- **Recent failures**: the latest videos whose last try failed, with the stage, the cause, the message, the library, the attempt and when.

To retry failures, go to the [Processing](/queue) page: they are grouped under **Needs attention**, or open the stage from **Stages** and press **Retry failed**. Click a failed video's title to see its history.

## What the states and messages mean {#states}

- **Live**: the page is updated as jobs finish.
- **Live updates paused, refreshing every 5 s**: the live connection dropped; the page still refreshes every few seconds.
- **Could not refresh; showing the last report**: the last refresh failed; the numbers you see may be old.
- **… × realtime**: how many seconds of audio are transcribed per second. 10× means one hour of audio takes six minutes.
- **… × per video**: the average speed of one video on its own. Several videos run at once, so the total speed is higher.
- **redone after a restart or failure**: audio that had to be transcribed again because a worker restarted or a job failed part way.
- **Done around …**: the expected finish time.
- **At the speed the workers decode now**, **At the speed of the last 10 minutes**, **At the speed of the last hour**: which speed the time left is based on. Maktaba uses the most recent speed it has.
- **Not known yet: nothing was transcribed in the last hour.**: there is no recent speed to estimate from.
- **Everything is transcribed**: nothing is left.
- **No open failures.**: no video's last try failed.
- **unknown cause**: the failure has no recorded cause (an older failure).
- **No library**: videos that belong to no library.

## FAQ {#faq}

### Why does the time left jump around? {#faq-eta}

It is the audio left divided by the most recent speed. When workers start, stop or pick up long videos, the speed changes and so does the estimate.

### Why are hours and videos at different percentages? {#faq-hours-videos}

Videos have different lengths. If the long ones are done first, the hours go faster than the video count, and the other way round.

### How do I make it faster? {#faq-faster}

Add a machine with a GPU that serves **Transcribe**, or change the **GPU** pool on the Transcribe queue's page. See [Processing](/help/processing#pools).

### Why does my Windows or Linux machine not transcribe? {#faq-windows}

While the second listen is on, transcription is left to the machines that run it (the Mac, and a PC whose NVIDIA GPU holds it beside Whisper: 10 GB or more on Windows, 8 GB or more on Linux, an RTX 20 series card or newer, a driver of R580 or later), so every transcript gets it. To let a Windows or Linux machine transcribe too, turn on **Transcribe without the second listen** in [Settings, Transcription](/help/rules#transcription): its transcripts keep Whisper's words, and a machine that runs the second listen gives them theirs later. If it cannot (it cannot read the file, or its model does not load), it tries again later, waiting longer each time; after 6 tries it stops and says so once, as a warning on that video's transcribe job in **Activity** on [Processing](/help/processing#what-it-is). Running transcribe on the video again starts the tries over. A machine whose own settings leave transcription out (its stage list) never transcribes.

The number of tries and the waits between them are in [Settings, Transcription](/help/rules#transcription). The wait counts from the end of each try. A transcript left as it is gets its tries again by itself with a new second-listen model, or when a machine runs the second listen with a new GPU or new packages; **Try the stopped transcripts again (round)** there gives them all their tries again. Deleted and missing videos are not tried.

### Why is a video transcribed but not searchable? {#faq-not-searchable}

**Search index** runs after **Transcribe**. Check its counters under **Stages**: the video may still be waiting there, or have failed.

### Can I stop transcription? {#faq-stop}

Yes, on the Processing page: open **Transcribe** and press **Pause this queue**, or leave it out in **Start a run**.

## When something goes wrong {#troubleshooting}

### Could not load the transcription report. {#load-error}

- **What it means:** the page could not reach the server.
- **Does Maktaba fix it itself?** No; the page does not retry on its own.
- **What you can do:** press **Try again**. If it keeps failing, check that the Maktaba server is running.

### Time left says Not known yet {#eta-unknown}

- **What it means:** nothing was transcribed in the last hour. Transcription is paused, waiting, or no worker serves it.
- **Does Maktaba fix it itself?** It starts again by itself once the queue is free to run.
- **What you can do:** open the **Transcribe** queue from **Stages** and read why it waits. See [Why a queue waits](/help/processing#waiting).

### A video fails to transcribe {#transcribe-failed}

- **What it means:** the last try of that video failed. The bold text is the cause; the rest is the message.
- **Does Maktaba fix it itself?** It already tried a few times with a pause between tries.
- **What you can do:** look up the cause in [Failures](/help/failures). The usual ones are [NAS read timed out](/help/failures#nas-timeout), [File is missing on disk](/help/failures#file-missing), [Could not decode the media file](/help/failures#ffmpeg-decode) and [other causes](/help/failures#other). Fix it, then retry from the [Processing](/queue) page.

### Subtitles fail but transcription is done {#subtitles-failed}

- **What it means:** the subtitle files could not be written next to the video, usually because the share is not writable or is busy.
- **Does Maktaba fix it itself?** It retries a few times. It never overwrites a subtitle file it did not write.
- **What you can do:** check that the share allows writing from the Maktaba machine, then retry the **Subtitles** stage from its queue page.
