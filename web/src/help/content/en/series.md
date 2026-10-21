# Series

Shows grouped into seasons and episodes, with your progress through each.

## What it is {#what-it-is}

Maktaba looks at your videos (their names, folders and dates) and groups the episodes of the same show into a series, with seasons and episode numbers. The [Series](/series) page lists every series in the libraries you may see. How shows and seasons are found is set in the rules (see [Detection](/help/rules#detect)).

## How to use it {#how-to-use}

### Browse series {#browse}

Each card shows the series name, its year when known, the number of seasons and episodes, and a bar with how many episodes you have watched. Pick **Sort**: **Name** or **Progress**. Click a card to open the series.

### Open a series {#series-page}

The series page shows one row per season, **Season 1**, **Season 2** and so on. **Specials** holds the extras that belong to no season. Each episode shows its number (E01, E02…), its title, a check mark once you have watched it, and a small bar when you stopped part way. Click an episode to open its page.

### Continue watching {#continue-watching}

Press **Continue watching** at the top of a series to play the next episode you have not finished. It appears once Maktaba knows which episode comes next for you.

### Accept all episode matches {#accept-all}

Admins see **Accept all episode matches**. It applies the best metadata match found for every episode of the series at once (titles, descriptions and so on). Anything you set by hand is kept. You see **Episode matches applied.** when it is done. To review matches one by one instead, use each episode's **Metadata** tab (see [Finding metadata](/help/metadata)).

## What the states and messages mean {#states}

- **3 of 10 watched** (for example): your progress through the series.
- **Missing**: a grey cell with a question mark is an episode number the series skips, for example E04 between E03 and E05. The file is not in your library.
- **Absolute numbering**: the series counts episodes from 1 upward without seasons, so episodes show as #1, #2…
- **Specials**: episodes outside the numbered seasons.
- **No series detected yet**: Maktaba has not grouped any episodes into a series yet. Series appear after processing.
- **No episodes in this series yet.**: the series exists but has no episodes linked to it.
- **Episode matches applied.**: **Accept all episode matches** finished.

## FAQ {#faq}

### Why is a show I have not listed as a series? {#faq-not-a-series}

Maktaba only groups videos when their names or folders show they are episodes of one show. Give the files clear names (show name and episode number), or change the [Detection](/help/rules#detect) rules.

### An episode is in the wrong season. How do I fix it? {#faq-wrong-season}

Open the episode, go to its **Metadata** tab and pick the right match (see [Finding metadata](/help/metadata)). If many episodes are wrong, the [Detection](/help/rules#detect) rules are the better place.

### What does a missing episode mean for me? {#faq-missing}

Only that the file is not in your library. Add it to the folder and Maktaba picks it up.

### Does Continue watching start where I stopped? {#faq-continue-position}

It opens the right episode. The player starts it from the beginning; jump ahead with a chapter or the transcript (see [Videos and the player](/help/videos)).

## When something goes wrong {#troubleshooting}

### Something went wrong. (the series do not load) {#load-error}

- **What it means:** The page could not read the series from the server.
- **Does Maktaba fix it itself?** No, the page waits for you.
- **What you can do:** Press **Try again**. If it keeps failing, check [System health](/help/system-health) or ask an admin.

### Accept all episode matches shows an error {#accept-all-error}

- **What it means:** The matches could not be applied, for example because you lack the right to edit this library.
- **Does Maktaba fix it itself?** No.
- **What you can do:** Read the message, then try again or accept the matches one by one on each episode's **Metadata** tab.

### Episodes are listed as missing that I do have {#false-missing}

- **What it means:** Maktaba did not link your file to the series, so the number looks empty.
- **Does Maktaba fix it itself?** It links it once the file is processed and recognised.
- **What you can do:** Check the file on the [Processing](/queue) page. If it is done but still not linked, rename it more clearly or pick the right match on its **Metadata** tab.
