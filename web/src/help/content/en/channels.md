# Channels

Build and edit Maktaba's live TV channels: what each channel plays and when, the clips between programmes, live broadcasts, the logo and the news-style strip.

## What it is {#what-it-is}

**Channels** (Admin, at [/admin/channels](/admin/channels)) lists every channel with its number, name, category, what is **On now** and how many hours the guide holds ahead (**Guide ahead**). From here you make a channel, open one to edit it, switch it on or off, reorder the lineup and delete channels. Only admins can open this page.

Most channels are written by the lineup plan. The **Source** filter tells them apart: **From plan** (the plan keeps it up to date), **Edited by you** (you changed it, so the plan leaves it alone) and **Made by hand**.

The page also shows the **XMLTV guide** address, the same one Plex and other players read (see [Live TV administration](/help/live-tv-admin#addresses)).

## How to use it {#how-to-use}

### The channel list {#list}

- Search by name, number or what is on now, and filter by **Category** or **Source**.
- Drag a row by its handle to reorder, or focus the handle and use the up and down arrow keys. Reordering changes the channel numbers. Clear the filters first: you cannot reorder a filtered list.
- The **On**/**Off** switch turns a channel on or off. **Edit** opens the editor, **Preview** opens it on the Preview tab, **Delete** removes the channel after you confirm.
- **Filler pools** at the top opens the clips that fill gaps (see [Filler pools](/help/filler)).

### Make a new channel {#new-channel}

1. Click **New channel**. It starts as a **Schedule** channel, switched off, with the next free number.
2. Fill in **Basics**, then build the **Schedule** (at least one slot).
3. Pick a logo, watermark and loading screen on **Art** if you like: they upload as soon as the channel is saved.
4. Click **Save**, then switch the channel on.

### Basics {#basics}

**Name**, **Channel number** (above 0, not used by another channel), **Category** (pick one or type a new one), **Programming mode**, **Transition** (**Hard cut** or **Crossfade**) and **Enabled**. The four modes:

- **Schedule**: a weekly grid of time slots, like real TV.
- **Shuffle**: plays the whole library in a fair shuffle.
- **Marathon**: plays one series in order, back to back.
- **Smart mix**: mixes by time of day using the library's classification.

On a saved channel, **Prefer host** picks the machine that should encode it (see [Live TV administration](/help/live-tv-admin#prefer-host)); it is saved at once. Rule changes apply from the next programme boundary, not immediately.

### The rule of a Shuffle, Marathon or Smart mix channel {#rule}

When the mode is not **Schedule**, the **Schedule** tab shows the channel's rule instead of a grid:

- **Marathon**: find the series, then choose the **Order** (**Aired order**, **DVD order**, **Filename order**) and whether to **Loop the series**.
- **Smart mix**: choose a **Daypart profile**. If the library's classification is not available, it falls back to a weighted shuffle.
- **Shuffle**: nothing to set; it plays every video of the library. Use **Schedule** mode to choose what plays when.

### The weekly schedule {#schedule}

- **Which grid**: **Regular**, plus an optional **Ramadan** grid (**Add a Ramadan grid**) that airs during Ramadan instead of the regular one.
- **Repeat**: **Same every day** or **By day of the week**.
- Click free time in the grid, or **Add slot**, to add a slot there. Click a slot to edit it. The colours are explained under **What the colours mean**.

### Slots {#slots}

A slot has **Starts**, **Ends** (00:00 means midnight, the end of the day), **Days** and what it **Plays**:

- **Plan lane**, **Series** or **Collection**: search and pick one, then choose **Next episode (in order)** or **Shuffle**.
- **Filler only**: the slot airs clips, bumpers and station idents only.
- **Live broadcast**: **Make this slot a live broadcast** stores it with the channel's live broadcasts.

**Filler pool for this slot** sets the pool the slot's gaps use first. **Split in two** halves a slot; **Delete slot** removes it. If two slots overlap on the same day, the earlier one wins.

### Filler {#filler}

The gaps between programmes are filled from the channel's filler lists:

- **Add a filler list** picks a pool. Each list has a **Weight** (a bigger weight is picked more often; the page shows about what share of the picks it gets) and a **Cooldown (minutes)** before it can be picked again.
- **Clip repeat cooldown (minutes)**: how long before the same clip may air again.
- **Longest filler gap (minutes)**, 0 to 240: a longer gap at the end of a slot takes the next episode instead, even if it runs over.
- **Share spare time out between a slot's episodes**: when on, a slot's spare time is shared out between its episodes.

With no lists, gaps use the slots' pools, then a still screen (the slate).

### Live broadcasts {#live}

Streams the channel carries at set times, such as the news on the hour. Nothing runs into a live broadcast; the slot's show goes on after it. Click **Add live broadcast** and fill in **Stream address** (starting with https://), **Title in the guide**, **From** and **Until**, **Every (minutes)**, **Length (minutes)** and **Days** (none means every day). **Remove** deletes one. They are saved with the channel when you click **Save**.

### Art {#art}

- **Logo**: PNG, JPEG, WebP, GIF or SVG, up to 5 MB.
- **Corner watermark** and **Loading screen**: PNG, JPEG, WebP or GIF (an animated GIF keeps moving), up to 5 MB.

Drop an image or click **Choose file**. On a saved channel the image uploads at once and is marked **Uploaded**; **Back to plan** removes it and returns to the plan's picture (**From the plan**). For the watermark, choose the **Watermark corner**, **Watermark width (% of the picture)** (1 to 50) and **Margin from the edge (%)** (0 to 25), then **Apply**.

### Channel strip {#strip}

On a saved channel the **Art** tab also holds the **Channel strip**: a news-style bar with what is on now, what is next and a clock that rotates between cities. What you leave as it is follows the strip settings in [Settings → Rules](/settings/rules) (see [Rules](/help/rules#tv-strip)).

- **Show the strip**: **As in Settings**, **On for this channel** or **Off for this channel**. When it is off, the preview still shows how it would look.
- Set **Where**, **Direction**, **Long titles** (pages that take turns, cut with …, or scroll across), **When**, sizes, **Colours** (or **Ready-made colours**) and the clocks (**Add a clock**, with a **Time zone** such as Asia/Riyadh and the city name in Arabic and other languages).
- The preview beside the form shows the result. Click **Save strip**, or **Use the Settings defaults** to drop this channel's own strip settings.

### Preview {#preview}

The **Preview** tab shows what the guide holds for the next 48 hours. **Refresh** reloads it. **Regenerate schedule** rebuilds the channel's schedule after you confirm: what is airing now finishes, and everything after it is planned again within a few minutes.

### Channels from the lineup plan {#plan}

A channel **From plan** says so at the top of the editor. If you change its settings, the button becomes **Save and stop following the plan**: after saving, the next lineup run leaves it alone. Art and live broadcasts do not count as changes. An edited channel shows **Back to plan**, which hands it back; the plan's settings replace yours on the next lineup run. Switching a plan channel on or off, or moving it in the list, asks you first for the same reason.

### Edits go into a TV change {#tv-draft}

When TV staging is on, nothing you save here goes on the air straight away. Your saves (new channels, edits, reordering, art, the strip, regenerated schedules) collect in a draft TV change. The editor says so: "Editing draft change #n: what you save goes on the air when the change is approved", with **Open change #n**. The change is then checked and approved on the Changes page. See [TV changes](/help/tv-changes).

## What the states and messages mean {#states}

- **From plan**: the lineup plan writes this channel and the next lineup run updates it.
- **Edited by you**: you changed it, so the lineup plan no longer updates it.
- **Made by hand**: made in this editor, not by the plan.
- **On now** with "{pct}% played": the programme airing now and how far into it the channel is. **Nothing scheduled**: the guide has nothing at this time.
- **Guide ahead**: how many hours of programmes the guide already holds.
- **Uploads on save**: a picture picked for a new channel, uploaded when you save.
- **Uploaded** / **From the plan**: whether the picture is your own or the plan's.
- A dot after a tab name (for example **Schedule •**): something on that tab needs fixing before you can save.
- **Editing draft change #n**: your saves go into that TV change, not on the air yet.

## FAQ {#faq}

### Why didn't my change show on the TV straight away? {#faq-not-live}

With TV staging on, your saves go into a draft TV change that must be checked and approved first (see [TV changes](/help/tv-changes)). Even without staging, a rule change applies from the next programme boundary.

### What is the difference between Shuffle and Smart mix? {#faq-shuffle-smart}

**Shuffle** plays every video of the library in a fair shuffle at any hour. **Smart mix** picks by time of day using the library's classification.

### What happens when a slot ends before its episode is over? {#faq-slot-end}

Spare time at the end of a slot is filled from the filler pools. If the gap is longer than **Longest filler gap**, the next episode plays instead, even if it runs over.

### How do I make a channel follow the plan again? {#faq-back-to-plan}

Open it and click **Back to plan**. Your settings are replaced by the plan's on the next lineup run.

### Can I have a different schedule during Ramadan? {#faq-ramadan}

Yes. On the **Schedule** tab click **Add a Ramadan grid** and fill it in. It airs during Ramadan instead of the regular grid.

## When something goes wrong {#troubleshooting}

### Name is required {#name-required}

- **What it means:** the channel has no name.
- **Does Maktaba fix it itself?** No. **Save** stays off until you fix it.
- **What you can do:** type a name on **Basics**.

### Enter a channel number above 0 / Number already used by … {#number-taken}

- **What it means:** the number is empty, 0 or below, or another channel already has it.
- **Does Maktaba fix it itself?** No.
- **What you can do:** pick a free number on **Basics**, or move the other channel first.

### Your copy is stale — reload and try again {#stale}

- **What it means:** the channel changed after you opened it (someone else, or the lineup plan, saved it), or the save would give two channels the same number.
- **Does Maktaba fix it itself?** No. Nothing was saved.
- **What you can do:** close the editor, reload the page, check the channel and its number, and make your change again.

### The schedule has no slots yet: add at least one {#no-slots}

- **What it means:** a **Schedule** channel needs at least one slot.
- **Does Maktaba fix it itself?** No. The channel cannot be saved without one.
- **What you can do:** add a slot on the **Schedule** tab, or pick another mode.

### A slot of the schedule needs fixing {#slot-fix}

- **What it means:** a slot has no start or end time, ends before it starts, or does not say what it plays.
- **Does Maktaba fix it itself?** No.
- **What you can do:** open the **Schedule** tab, click each slot and fix the field its editor names.

### This slot overlaps another on the same day {#slot-overlap}

- **What it means:** two slots share time on the same day.
- **Does Maktaba fix it itself?** Yes, in a way: the earlier slot wins. This is a warning and does not block saving.
- **What you can do:** move or shorten one slot if that is not what you meant.

### Pick the series this channel plays {#pick-series}

- **What it means:** a **Marathon** channel has no series.
- **Does Maktaba fix it itself?** No.
- **What you can do:** search and pick a series on the **Schedule** tab.

### Pool not found {#pool-not-found}

- **What it means:** a filler list points to a pool that was deleted.
- **Does Maktaba fix it itself?** No. That list adds no clips.
- **What you can do:** remove the list on the **Filler** tab and add another pool.

### Live broadcasts could not be loaded / could not be saved {#live-failed}

- **What it means:** Maktaba could not read or write the live broadcasts. On save, the rest of the channel may already be saved.
- **Does Maktaba fix it itself?** No.
- **What you can do:** check the stream address (a full https:// address), the length (1 to 1440 minutes) and **Every** (at least 1 minute), then click **Save** again. If loading fails, reload the page.

### The image could not be uploaded {#art-upload}

- **What it means:** the file type is not accepted (SVG works only for the logo), the file is larger than 5 MB, or the upload failed.
- **Does Maktaba fix it itself?** No.
- **What you can do:** use a PNG, JPEG, WebP or GIF under 5 MB and try again.

### Strip values out of range {#strip-errors}

- **What it means:** a strip field shows "From … to …", "A colour as #RRGGBB" or "Not a time zone".
- **Does Maktaba fix it itself?** No. **Save strip** waits for valid values.
- **What you can do:** use a number in the range shown, a colour such as #0b5d3b, and a time zone name such as Africa/Cairo.

### Could not reorder — number collision {#reorder-error}

- **What it means:** the new order would give two channels the same number, often because the list changed meanwhile.
- **Does Maktaba fix it itself?** The list reloads with the saved order.
- **What you can do:** try the move again.
