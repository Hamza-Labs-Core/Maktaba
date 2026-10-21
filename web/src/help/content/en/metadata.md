# Finding metadata

Maktaba can suggest a better title, description and poster for a video, and tell you what else it found (series, speakers, the book a lesson explains). Nothing changes until you accept a suggestion.

## What it is {#what-it-is}

Each video has a **Metadata** tab. There Maktaba shows its suggestions for the video. A suggestion comes from one of these:

- **Maktaba's own intel**: what Maktaba already knows: file and folder names, the series and its other episodes, the book the series explains, chapters, transcripts.
- **Web search** and **Language model**: a web search whose pages a language model reads, when your admin has set them up.

Every suggestion has a match percentage, and every proposed value has its own confidence and the sources it rests on. You decide: **Accept** writes the proposed values to the video, **Dismiss** hides the suggestion. An accepted change can be undone with **Revert to original**.

Only admins can search, accept, dismiss or revert.

## How to use it {#how-to-use}

### Find metadata for a video {#find-metadata}

1. Open the video. If it has no web metadata yet, the **Watch** tab says "No web metadata yet for this video." Press **Find metadata**, or open the **Metadata** tab.
2. Press **Search again from what Maktaba knows** to search with what Maktaba already knows about the video.
3. Or type your own words (title, series, book, speaker) in the box and press **Search manually**.
4. You see "Searching…". The suggestions appear when the search is done; you can leave the page meanwhile.

### Read a suggestion {#read-a-suggestion}

The best suggestion is on top: "We found this might be …" with its **% match** and where it came from. Below it:

- **Current vs. proposed**: each field (**Title**, **Description**, **Poster**) with its current value, the proposed one and its **Confidence**. The numbers in brackets point to the sources.
- **Won't change**: you or another admin set this field by hand, so accepting leaves it alone.
- **Also found**: other facts, such as the series, season, presenter or book. They are for your information; accepting does not write them to the video.
- **Sources**: the web pages (they open in a new tab) and the kinds of Maktaba's own knowledge used, each with what it was used for.

Weaker suggestions are listed under **Other suggestions**.

### Accept or dismiss {#accept-dismiss}

- **Accept** writes the proposed title, description and poster to the video, except fields marked **Won't change**. You see "Metadata applied."
- **Dismiss** hides the suggestion: "Suggestion dismissed." A new search brings dismissed suggestions back.

### Undo an accepted suggestion {#revert}

After an accept, the tab says "An accepted match changed: …" and names the fields. Press **Revert to original** to put the earlier values back. You see "Restored the original values.", or "Nothing left to revert." if there was nothing to undo.

### Review many videos at once {#review-queue}

Admins can open [Review matches](/admin/enrichment). It lists every video with a suggestion waiting for a decision, with the suggested title and its match percentage. Pick a video to see its full suggestion on the right. Keys: **j** and **k** move through the list, **a** accepts, **d** dismisses; the next video is selected for you. When the list is empty you see "Nothing to review".

### Let Maktaba accept sure matches {#auto-accept}

In [Settings → Rules](/settings/rules), section [Finding metadata](/help/rules#enrich), **Accept matches by themselves from (confidence)** accepts a waiting match at or above that confidence, checked every minute. 0 (the default) means off: every match waits for you. Fields set by hand are never replaced, and you can still revert. **Find metadata for new videos by themselves** (off by default) searches for a few new videos at a time on its own.

### On-air titles {#display-titles}

The video page also shows the **On-air title**: the short, clean title the TV guide, the channel strip and dizqueTV use. A badge says where it came from: **by the rules**, **by the language model** or **set by hand**. Admins can press **Change** to write their own; a title set by hand always wins. **Back to the automatic title** removes it, and "(automatic: …)" shows what the rules would use. "not made yet" means Maktaba has not made one for this video yet. The rules for these titles are in [Display titles](/help/rules#display-titles).

### Daily caps {#daily-caps}

Web searches, pages read, language model questions and spending have daily caps (section [Finding metadata](/help/rules#enrich) of the rules). When a cap is reached, the web part waits for the next day. Maktaba's own suggestion is still made.

## What the states and messages mean {#states}

- **Searching…**: a search for this video is queued or running. The page checks every few seconds.
- **No suggestions**: nothing found yet. Search with other words, or press **Search again from what Maktaba knows**.
- **n% match**: how sure Maktaba is that the suggestion is this video. Suggestions below the minimum confidence in the rules are not kept.
- **Won't change**: a field set by hand; accepting skips it.
- **The web search waits until …**: a daily cap or the search service's limit was reached; see [below](#web-search-waits).
- **The last search failed: …**: see [below](#search-failed).
- **Metadata applied.** / **Suggestion dismissed.** / **Restored the original values.**: your action worked.

## FAQ {#faq}

### Does Maktaba change my videos by itself? {#faq-automatic}

No, unless an admin turned on **Accept matches by themselves from (confidence)**. Even then, fields set by hand are never replaced, and every accepted change can be reverted.

### Why is there only "Maktaba's own intel" and never a web suggestion? {#faq-no-web}

The web search or the language model is not set up or is switched off. An admin can check **Web search** and **Language model** in [Finding metadata](/help/rules#enrich) and the Web search extension on [Extensions](/help/extensions).

### Why did Accept not change the series or the season? {#faq-also-found}

Accept writes the title, description and poster. The facts under **Also found** are shown to help you decide.

### I dismissed a suggestion by mistake. {#faq-dismissed}

Search again (**Search manually** or **Search again from what Maktaba knows**). Dismissed suggestions come back.

### Where do I find the review page? {#faq-review-page}

It is at [Review matches](/admin/enrichment), for admins. It is not in the menu yet.

## When something goes wrong {#troubleshooting}

### No suggestions {#no-suggestions}

- **What it means:** the search found nothing it is sure enough about.
- **Does Maktaba fix it itself?** No.
- **What you can do:** search manually with the series name, the book or the speaker. Videos with transcripts and chapters give Maktaba more to work with; see [Transcription](/help/transcription).

### The web search waits until … {#web-search-waits}

- **What it means:** today's web searches, page reads, model questions or spending cap are used up, or the search service's own limit was reached.
- **Does Maktaba fix it itself?** Yes. The web part runs again after the time shown. Maktaba's own suggestion is already below.
- **What you can do:** wait, or have an admin raise the caps in [Finding metadata](/help/rules#enrich).

### The last search failed: … {#search-failed}

- **What it means:** the search kept failing, for example because the web search or the model server did not answer.
- **Does Maktaba fix it itself?** It retries a few times with growing pauses before it gives up.
- **What you can do:** check the reason, the [Extensions](/help/extensions) page and the model server in [Tag check](/help/rules#tagcheck), then press **Search again from what Maktaba knows**. The search also shows as the [enrich](/help/stages#enrich) step on [Processing](/help/processing).

### "Searching…" never ends {#searching-stuck}

- **What it means:** the search is waiting for its turn, or retrying after an error.
- **Does Maktaba fix it itself?** Yes; queued searches are picked up within a minute and retried.
- **What you can do:** check [Processing](/help/processing) to see whether processing is paused or the step is waiting.

### An error notice after Accept, Dismiss or a search {#action-refused}

- **What it means:** the server refused the action, for example because you are not an admin or the suggestion was already accepted.
- **Does Maktaba fix it itself?** No.
- **What you can do:** reload the page to see the current state, then try again. Ask an admin if you are not one.
