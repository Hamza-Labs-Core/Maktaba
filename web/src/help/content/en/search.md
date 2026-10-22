# Search and YouTube

Find a video by its title or by something said in it, and look it up on YouTube.

## What it is {#what-it-is}

[Search](/search) looks through the titles of your videos and through their transcripts, so you can find the exact moment a word or idea is spoken. It can also show matching videos from YouTube next to your own results.

You only find videos from the libraries you may see.

## How to use it {#how-to-use}

### Search {#search-basics}

1. Type in the box at the top of any page, or on the [Search](/search) page. Suggestions appear as you type.
2. Press Enter or **Search**.
3. The results come in two lists (see below). Click a passage's time to play the video from that moment, or a video's title or picture to open its matches page.

### The two lists {#results-lists}

- **In titles** lists the videos whose title matches, best match first.
- **In transcripts** lists each video whose words match, once, with its library, its length, how many times it matches (**12 matches**) and its best passages, each with its time range, for example [12:40 → 13:05]. **Also in title** means the video's title matches too.
- **Show all 12** under a video opens every matching passage of that video, best first; **Load more** brings the next ones and **Show fewer** folds them again.

### List or grid {#list-grid}

**List** shows a row per video with its library and passages. **Grid** shows a card per video with its picture, title and length; on a transcript card, **Show passages** opens its passages. Your choice applies to both lists and is kept in this browser.

### Pages {#pages}

Each list has its own pages. Under a list you see **Page 2 of 5 · 93 videos** and the page buttons. The pages are kept in the address, so Back, Forward, a reload or a shared link show the same page in the same order.

### The matches page of a video {#matches-page}

Click a video's title or picture to open its matches page. It shows:

- the player, with a mark on the progress bar for every match;
- **Previous match** and **Next match**, which jump from where the video is playing;
- your search words, with **Back to results** to return to the same pages;
- **All matches**, every match in time order with its time and the matching words. A **Title** row means the title matches; **Transcript** rows are passages. Click one to play from there. A video that matches only by its title says **No matches in the transcript**.

### Recent searches {#recent-searches}

**Recent searches** on the side lists your last searches, newest first, with their mode, when you ran them and how many videos they found in titles and in transcripts. **Results kept** opens the same results in the same order; **Expired, runs again** runs the search again. Remove one with the button next to it, or all of them with **Clear all**. Running the same words and mode again replaces the older entry.

### How long results are kept {#kept-results}

Maktaba keeps each search's results for 15 minutes after you last used them (opening a page counts as use). After that, opening them runs the search again: you stay on the same pages if they still exist, and a note says **These results were refreshed; the order may have changed.** with **Back to page 1**.

### Choose a mode {#modes}

The **Mode** list next to the box changes how Maktaba matches your words. Your choice is kept in this browser.

- **Hybrid**: the default. Mixes the two below and usually gives the best results.
- **Keyword**: the exact words you typed. Best for a name or a precise phrase.
- **Semantic**: the meaning. Finds passages that talk about the same idea in other words.

### Save a search {#saved-searches}

Press **Save** to keep the current search and its mode. It appears under **Saved searches** on the side, apart from **Recent searches**. Click it to run it again, or delete it with the button next to it.

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
- **12 matches**: how many passages of the video's transcript match.
- **Also in title**: the video is in **In titles** too.
- **These results were refreshed; the order may have changed.**: the results were older than 15 minutes, so the search ran again. **Back to page 1** goes to the first pages.
- **Results kept** / **Expired, runs again**: whether a recent search opens its kept results or runs again.
- **No matches in the transcript**: on a matches page, the video matches by its title only.
- **Searching YouTube…**: the YouTube part is still loading; your own results are already shown.
- **No YouTube results.**: YouTube found nothing for these words.
- **YouTube search is off.**: this server cannot search YouTube right now.
- **YouTube is rate limited — try again shortly.**: YouTube asked Maktaba to slow down.
- **YouTube is unavailable right now.**: the YouTube search failed this time.
- **Metadata staged for "…". Review it on the video's Metadata tab.**: the import worked; go accept it on the video.

## FAQ {#faq}

### Why can I not find a video I know is there? {#faq-not-found}

A new video can be found by its title right away, but its words only after it is transcribed. Check [Transcription](/help/transcription). Also try **Hybrid** or **Semantic** mode.

### Why did the order of my results change? {#faq-order-changed}

The results were kept for 15 minutes after you last used them, then run again. New transcripts or titles since then can move videos up or down.

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
