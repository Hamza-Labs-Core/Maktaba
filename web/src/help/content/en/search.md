# Search and YouTube

Find a video by its title or by something said in it, and look it up on YouTube.

## What it is {#what-it-is}

[Search](/search) looks through the titles of your videos and through their transcripts, so you can find the exact moment a word or idea is spoken. It can also show matching videos from YouTube next to your own results.

You only find videos from the libraries you may see.

## How to use it {#how-to-use}

### Search {#search-basics}

1. Type in the box at the top of any page, or on the [Search](/search) page. Suggestions appear as you type.
2. Press Enter or **Search**.
3. **Videos** lists videos whose title matches. **In transcripts** lists the passages where your words are said, each with its time range, for example [12:40 → 13:05].
4. Click a passage to play the video from that moment, or a video to open its page.

### Choose a mode {#modes}

The **Mode** list next to the box changes how Maktaba matches your words. Your choice is kept in this browser.

- **Hybrid**: the default. Mixes the two below and usually gives the best results.
- **Keyword**: the exact words you typed. Best for a name or a precise phrase.
- **Semantic**: the meaning. Finds passages that talk about the same idea in other words.

### Save a search {#saved-searches}

Press **Save** to keep the current search and its mode. It appears under **Saved searches** on the side. Click it to run it again, or delete it with the button next to it.

### Also search YouTube {#youtube}

When the server can search YouTube, a **Also search YouTube** box appears next to **Save**. Tick it and search: a **From YouTube** section shows matching YouTube videos with their channel and views. Click a result to open it on YouTube.

- **In your library →** means you already have that video. Click it to open your copy.
- **Imported →** means its details were already brought into one of your videos.

### Import details from YouTube {#youtube-import}

Admins see **Import** on the other YouTube results. Use it when a video in your library is that YouTube video but lacks a good title or description.

1. Press **Import**.
2. Under **Videos in your library**, pick the matching video, or use **Find a video by title** to look it up.
3. Press **Import**.

The YouTube title, channel, description and thumbnail are added as a suggestion on that video's **Metadata** tab. Nothing changes until you accept it there (see [Finding metadata](/help/metadata)).

## What the states and messages mean {#states}

- **No results. Check spelling or try a different mode.**: nothing matched. Try other words, or **Semantic** for ideas rather than exact words.
- **Searching YouTube…**: the YouTube part is still loading; your own results are already shown.
- **No YouTube results.**: YouTube found nothing for these words.
- **YouTube search is off.**: this server cannot search YouTube right now.
- **YouTube is rate limited — try again shortly.**: YouTube asked Maktaba to slow down.
- **YouTube is unavailable right now.**: the YouTube search failed this time.
- **Metadata staged for "…". Review it on the video's Metadata tab.**: the import worked; go accept it on the video.

## FAQ {#faq}

### Why can I not find a video I know is there? {#faq-not-found}

A new video can be found by its title right away, but its words only after it is transcribed. Check [Transcription](/help/transcription). Also try **Hybrid** or **Semantic** mode.

### Can I search in Arabic? {#faq-arabic}

Yes. Type in the language that is spoken in the video. Arabic and English both work.

### Why is there no "Also search YouTube" box? {#faq-no-youtube-box}

The server cannot search YouTube (the tool it uses is not installed there). Ask an admin.

### Does importing from YouTube download the video? {#faq-import-download}

No. It only brings the title, channel, description and thumbnail, as a suggestion you review.

## When something goes wrong {#troubleshooting}

### Something went wrong. (no results load) {#search-error}

- **What it means:** The search could not run on the server.
- **Does Maktaba fix it itself?** No.
- **What you can do:** Search again. If it keeps failing, check [System health](/help/system-health) or ask an admin.

### YouTube is rate limited — try again shortly. {#youtube-rate-limited}

- **What it means:** YouTube is refusing more searches from this server for a while.
- **Does Maktaba fix it itself?** Yes, it passes after a short time. Your own results are not affected.
- **What you can do:** Wait a few minutes and search again. If it lasts, see [YouTube asked to confirm you are not a bot](/help/failures#youtube-bot-check).

### YouTube is unavailable right now. {#youtube-unavailable}

- **What it means:** The YouTube search failed this time, for example because the network or YouTube did not answer.
- **Does Maktaba fix it itself?** The next search tries again.
- **What you can do:** Search again later. Your own results are not affected.

### The import shows an error {#import-error}

- **What it means:** The YouTube details could not be added to the video you picked.
- **Does Maktaba fix it itself?** No.
- **What you can do:** Read the message and try again, or search for the details on the video's **Metadata** tab instead.
