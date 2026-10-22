# Rules

The rules decide how Maktaba finds shows, tags them, plans the channels and runs the TV. You find them in [Settings → Rules](/settings/rules).

## What it is {#what-it-is}

The rules page has one tab per section (Channels, Detection, Tagging, Maktaba TV, Updates and so on). Each section groups its settings under headings. Every setting shows its name, a short explanation and its built-in default.

Only what you change is saved. Everything else keeps the built-in default, so a new Maktaba version can improve a default you never touched. A change applies from the next time its step runs.

Only admins can change the rules. Other users see the same page read-only.

## How to use it {#how-to-use}

### Find a setting {#find-a-setting}

1. Open [Settings → Rules](/settings/rules) and pick the section tab.
2. Type in the **Find a rule** box. It matches the setting's name, its explanation and its group, in the open tab only.
3. If nothing shows, you see "No rule matches …". Try another tab or a shorter word.

The tab label shows how many settings of that section are **changed** and **unsaved**. Each heading inside a section shows the same counts, so you can fold groups away with **Collapse all** and open only what you need.

### Change and save {#edit-and-save}

1. Edit the value. The setting is marked **unsaved**.
2. Fix anything marked in red. **Save** stays off while a typed value is wrong ("Fix n setting(s) marked below before saving").
3. Press **Save**. You see "Saved. n rule(s) differ from the defaults."

**Discard changes** drops every unsaved edit of the open section. **Undo change** on one setting drops just that edit. Your edits stay while you move between tabs, until you save or discard them.

Some settings are lists: type a value and press Enter or a comma to add it. Some are tables: use **Add row**, and the row menu to duplicate or remove a row.

### Go back to the default {#reset-to-default}

A setting you saved earlier is marked **changed**. Press **Back to default** next to it. It is saved at once and you see "Back to the default."

### TV settings go through TV changes {#tv-settings-staging}

When **Stage every TV change** is on (section [TV staging](#tv-staging)), a save in **Maktaba TV** or **Maktaba TV output** does not go on the air straight away. You see "Saved to draft change #n" and an **Open the change** link. The change is checked and goes on the air when it is approved on the [TV changes](/help/tv-changes) page. Until then the rules page keeps showing the values on the air.

### Previews on some tabs {#preview-panels}

- **Channels**: **Preview channels** plans the channels with the saved settings and shows what would be added, removed or changed. Nothing is written. Save first: the preview never uses unsaved edits.
- **Channel strip**: ready-made colours and a live preview of the strip with your edits, saved or not.
- **Maktaba TV away from home**: this month's remote use and spend.

## The sections {#sections}

Each entry says what the section decides and which settings people change most.

### Channels {#planner}

Decides which channels exist and how their days are built: themes, day parts, slots, filler and the Quran channel. Groups: Channels, Grid and slots, Ramadan, Programmes and filler, Fillers, Quran at dawn and Friday khutbah, How often shows air, Quran channel, Prime time, dizqueTV.

- **Themed channels** is the list of theme channels: number, names, the topics and audiences each one takes, and what it never takes.
- **Channels (most)** caps the lineup (20). **Days without a repeat** (30) decides how much content a channel needs: lower it and smaller themes get a channel.
- **Day parts** sets the windows of the day, and **Presenter channels** gives a large presenter his own channel.

Use **Preview channels** before you save big changes. See [Channel editor](/help/channels) and [Filler pools](/help/filler).

### Grouping {#groups}

Decides how leftover videos become clip collections, stand-alone programmes, promos and the questions and fatwas group. **Clip up to (min)** (9) splits clips from stand-alone programmes. **Question / fatwa phrases** and **Promo words** are lists of title words. Changes show in [Series](/help/series) and in the filler the channels use.

### Detection {#detect}

Decides how shows, seasons and talk shows are found from titles and playlists. Groups: Names and playlists, Seasons, Matching loose videos, Ongoing programmes, Talk shows.

- **Words that never name a show** stops common words ("full", "hd") from becoming a show name.
- **Smallest season (episodes)** (3) and **Shortest episode (s)** (180) decide what counts as a season and an episode.
- **Match score** (0.62) decides how sure Maktaba must be to put a video with no playlist into a season.

A change shows the next time detection runs. See [Series](/help/series) and [Stages](/help/stages#detect).

### Tagging {#tag}

Decides the topic, format, audience and occasion tags of each show, and what transcripts add. Groups: Evidence weights, Tags, Transcripts, Recitation in transcripts. **Lowest tag score** (0.3) drops weaker tags; raise it for fewer, surer tags. The weights decide how much the name, playlists, episode titles and transcripts count. Tags decide which channel a show lands on. See [Stages](/help/stages#tag) and [Transcription](/help/transcription).

### Tag check {#tagcheck}

A local language model (Ollama) reads each series' name and episode titles and says what it is about. Tags it does not confirm are weakened. **Check tags with a model** turns it on or off. **Model server (Ollama)** and **Model** must point to a server that runs a model that reads Arabic well. **Check clips too** also checks short videos used as filler. See [Stages](/help/stages#check-tags).

### Books {#books}

A lesson series that explains a book (for example a commentary on Bulugh al-Maram) takes its topics from that book. **Look up the books series explain** searches the encyclopedia (Arabic Wikipedia by default). A book not found is searched again after 30 days. See [Stages](/help/stages#books).

### dizqueTV export {#dizquetv}

Decides how the planned channels are written into dizqueTV.

- **Remove what the plan no longer has** deletes channels and lists Maktaba made earlier that the plan dropped.
- **Also remove other channels not in the plan** also deletes channels made by hand. Use it only for a fresh lineup.
- **Take over what exists** replaces a channel of the same number or name that Maktaba did not make.
- **Rewrite without a break** (on) lets the programme on air finish. **Only these channels** limits the export to some channel numbers.

See [dizqueTV](/help/dizquetv).

### Maktaba TV {#tv}

Decides how Maktaba's own channels air: the filler between programmes, the Ramadan grids, live broadcasts, loudness and how far ahead the guide reaches. People most often change **Live broadcasts** (a live stream a channel carries at set times), **Hijri date adjustment** (when Ramadan starts a day earlier or later where you are) and **Guide days ahead** (7). While [TV staging](#tv-staging) is on, a save here becomes a draft TV change. See [Live TV administration](/help/live-tv-admin).

### Maktaba TV output {#tv-output}

Decides how a channel on air is encoded. **Video encoder** (auto picks the hardware encoder when it works), **Picture size** (1920x1080) and **Video bitrate (kbit/s)** (6000 suits 1080p, 3000 suits 720p) set the picture. **Channels on air at once** limits each streaming machine. A lower bitrate saves upload, which matters for viewers away from home. While [TV staging](#tv-staging) is on, a save here becomes a draft TV change. See [Live TV administration](/help/live-tv-admin).

### Maktaba TV machines {#tv-cluster}

Decides which machines encode channels and how the home spreads them. **Other machines may stream** is off by default; each machine also needs "Stream on this machine" on the Live TV page. **Where channels go**: home_first fills the home first, balanced always uses the machine with the most room. **Move channels off a machine that stops** keeps a watched channel going elsewhere. See [Machines](/help/machines) and [Live TV administration](/help/live-tv-admin).

### Maktaba TV away from home {#tv-remote}

Decides who may watch channels away from home through Cloudflare Stream, and the limits. **Who may watch away from home**: all signed-in users, admins only, or the users listed. **Remote streams per user** (2) and **Remote channels at once** (22) cap use. **Monthly budget (USD)** (10) and **At 100 % of the budget** decide what happens when the money runs out: refuse new remote viewers, or only warn. The tab shows this month's use. See [Watching away from home](/help/remote-tv).

### Simulcast {#tv-simulcast}

Decides whether channels are also sent on to YouTube, Facebook or any RTMP address. **Simulcast to YouTube, Facebook and RTMP** is off by default and needs Cloudflare Stream. **Output-hours a month** (60) caps the cost: one always-on output is about 720 hours a month. You attach destinations to a channel on the Live TV page. See [Simulcast](/help/simulcast).

### TV staging {#tv-staging}

Decides how TV changes reach the air. **Stage every TV change** (on) prepares each lineup, schedule, settings or art change next to what is on the air, checks it, and waits for approval. For each kind of change you pick approval (an admin approves), quiet_window (it goes on in the **Quiet window**, 03:00-05:00 by default, if there are no warnings) or immediate. **When a channel switches**: boundary waits for the end of the programme on the air. **Roll back automatically** and **Hot standby** protect the air after a switch. See [TV changes](/help/tv-changes).

### Channel strip {#tv-strip}

The news-style bar across the picture: what is on now and next, and a clock. **Show the strip on every channel** is off by default. The tab has ready-made colours and a live preview. **Long titles** chooses pages that take turns, a cut title, or a scrolling ticker. **When** shows it always, or at each programme's start and every few minutes. **Clocks** lists the cities the clock rotates through. See [Channel editor](/help/channels).

### Finding metadata {#enrich}

Decides how **Find metadata** and **Search again from what Maktaba knows** work: Maktaba's own knowledge, a web search and a language model, and the daily caps. **Find metadata for new videos by themselves** is off by default. **Web search** and **Language model** choose the helpers. The daily caps (searches, pages read, model questions, spend) stop the web part until the next day. **Accept matches by themselves from (confidence)** is 0 (off) by default. See [Finding metadata](/help/metadata).

### Display titles {#display-titles}

Decides the short, clean on-air titles of the guide, the channel strip and the dizqueTV export: no channel names, hashtags, emojis, links or dates. **Longest title (characters)** (0: no limit) cuts long titles at a word. **Words removed** and **Patterns removed** list what is taken out. **Show the episode number** adds the episode label. A title you set by hand on a video always wins. See [Finding metadata](/help/metadata#display-titles).

### Exact dates {#exactdates}

**Fetch exact upload dates** (off by default) asks YouTube for the exact date of series videos whose date is only known to the month. Exact dates help Maktaba see whether a show is daily or weekly. It is slow on purpose; **Seconds between requests** sets the pace. A YouTube bot check stops the run and the next run carries on; **Use a signed-in browser** can help. See [YouTube bot check](/help/failures#youtube-bot-check).

### Channel audit {#audit}

**Audit the channels** (on) checks, after the channels are written, that what each channel airs is about the channel. It only reads dizqueTV and writes nothing. **Days weighed** (28) is how far ahead it looks. See [dizqueTV](/help/dizquetv).

### Plex Live TV {#plexlive}

Decides how Maktaba's channels appear in Plex. **Add Maktaba to Plex by itself** is off: then only the **Add Maktaba to Plex** button does it. **Refresh the guide in Plex** and **Refresh the channels in Plex** (both on) keep Plex up to date. Set **Tuner address for Plex** when Plex cannot reach Maktaba at its public address. **Check Plex's channel list every (min)** (15; 0: off) checks that another program (dizqueTV's Plex refresh) has not replaced Maktaba's channels in Plex and puts them back; after **Warn after this many restores in a row** (3) restores, each within **Restores count as in a row within (hours)** (24) of the previous, the Plex card advises turning off dizqueTV's channel mapping (see [Plex](/help/plex#channel-map-drift)). See [Plex](/help/plex).

### Broken files {#repair}

**Repair broken downloads** (off by default) downloads again a YouTube file that cannot be read, checks it and swaps it in. **Keep the broken file** keeps the old one beside it. **Tallest picture** (1080) and **H.264 first** decide what is downloaded. See [Failures](/help/failures#ffmpeg-decode).

### Extension relay {#relay}

Other machines run the Plex steps through the home, which adds the Plex sign-in so it never leaves the home. **Relay extension calls for machines** (on) turns this on; off, those steps run on the home only. The other settings limit how many calls each machine may make. See [Machines](/help/machines) and [Plex](/help/plex).

### Canonical share paths {#storage}

The home records the storage servers (NAS) it mounts and rewrites stored file paths to one form every machine understands. **Convert paths automatically** and **Register storage servers** are on. While paths are rewritten, the steps that read or write the shares pause (**Pause these pools**); the rest keeps running. A backup is taken first. See [Machines](/help/machines).

### Updates {#updates}

Decides how new releases are installed. **Install updates automatically** (on) installs in the **Quiet window** (03:00-05:00), the home first, then each machine. **Release channel**: stable or beta. **Release age before installing** (24 hours) skips releases that get pulled. **Stay at or below version**, **No automatic update before** and **Skipped versions** hold updates back. See [Updates](/help/updates).

### Machines {#cluster}

Decides how the machines of your cluster act as one Maktaba. **One sign-in for every machine** (on) lets you move from one machine to another without signing in again. **Home unreachable after (s)** (30) is how long a machine waits before it stops serving signed-in pages when it cannot reach the home. See [Machines](/help/machines).

### Transcription {#transcription}

Decides which machines transcribe. While the second listen is on, only the machines that run it (the Mac, and a PC whose NVIDIA GPU holds it beside Whisper) transcribe, so every transcript has it. **Transcribe without the second listen** (off) lets the other Windows and Linux machines transcribe too, with Whisper alone: faster, and a machine that runs the second listen gives those transcripts theirs later, a few at a time, after the new files. See [Transcription](/help/transcription#faq-windows).

**Second-listen tries per transcript** (6), **Wait after a try (hours)** (1) and **Longest wait between tries (hours)** (24) decide how a machine that runs the second listen gives a transcript made without it another go when it cannot give it its own: the wait counts from the end of each try and doubles after each, and after the last try the transcript is left as it is. It gets its tries again by itself with a second-listen model the home has not used before, or when a machine runs the second listen on a GPU, driver or packages the home has not seen; a try that found no room on the GPU does not count. Raise **Try the stopped transcripts again (round)** (0) by one to give them all their tries again yourself. Deleted and missing videos are not tried.

### Queue health {#queue-health}

Decides how often a failure is tried, what waits instead of failing, and when a queue turns red. See [Failure classes](/help/failures#classes).

- **Tries of a passing failure** (5): how many times a passing problem (a NAS timeout, a partial read) is tried before the item needs you. A step that runs once by design keeps its one try.
- **Check every (seconds)** (60) and **Check waits (seconds)** (5): how often the model, the shares and the free disk space are checked, and how long one check waits. **Waiting jobs retried after (hours)** (24): a waiting job is tried again after this even if no check released it.
- **Free disk needed (GB)** (5): below this, jobs that ran out of disk keep waiting. **Shared memory wait (minutes)** (10): how long jobs that ran out of Postgres shared memory wait.
- **Look back (hours)** (6), **Failures that stay neutral (share)** (0.02) and **Red from (jobs)** (10) decide when Maktaba counts a queue as failing: only when many recent jobs fail the same new way. A queue tile turns **Failing** only then; a few failed items show as a small count instead. See [Queue tile turns Failing](/help/failures#tile-failing).
- **Failure classes**: rows of a signature (the start of a failure's cause code, for example `nas_timeout` or `model_unavailable`) and a class (transient, item_permanent, unknown, dependency:… or resource:…). They are checked before the built-in classes.

## What the states and messages mean {#states}

- **changed**: the saved value differs from the built-in default.
- **unsaved**: you edited the value and have not saved it yet.
- **Default: …**: the built-in value that applies when you change nothing.
- **n to fix**: settings in this group hold a value that cannot be saved.
- **Saved. n rule(s) differ from the defaults.**: the save worked; n is the total in this section.
- **Saved to draft change #n**: a TV setting was saved into a TV change; it goes on the air when approved.
- **Back to the default.**: the setting now uses the built-in value.
- **Only admins can change the rules.**: you are not an admin; you can only read.

## FAQ {#faq}

### When does a change take effect? {#faq-when}

From the next time its step runs; you can see the steps and when they run on [Processing](/help/processing). TV settings go on the air through [TV changes](/help/tv-changes) while TV staging is on.

### How do I undo everything I changed in a section? {#faq-undo-all}

Unsaved edits: **Discard changes**. Saved changes: press **Back to default** on each setting marked **changed**.

### Why does the search not find a setting I know exists? {#faq-search}

The search looks only in the open tab. Open the right section first; the list of [sections](#sections) above helps.

### Will a new Maktaba version change my settings? {#faq-upgrade}

No. Your saved values stay. Settings you never changed follow the new defaults.

### Some setting names are in English. Why? {#faq-english}

Not every setting has a translation yet. The name in the app is the one this guide uses.

## When something goes wrong {#troubleshooting}

### Could not load the rules. {#load-error}

- **What it means:** the page could not read the rules from the server.
- **Does Maktaba fix it itself?** No; the page does not try again by itself.
- **What you can do:** reload the page. If it keeps failing, check [System health](/help/system-health).

### Not saved: … {#not-saved}

- **What it means:** the server refused the save; the message names the setting and the reason, and that setting is marked.
- **Does Maktaba fix it itself?** No. Nothing of that save was written.
- **What you can do:** correct the marked value and press **Save** again.

### Fix n setting(s) marked below before saving {#fix-errors}

- **What it means:** a value you typed is not allowed, for example "Enter a whole number.", "Must be at least …", "Enter a time as HH:MM" or "Enter a colour as #RRGGBB".
- **Does Maktaba fix it itself?** No. **Save** stays off until it is fixed.
- **What you can do:** correct the value, or press **Undo change** on that setting.

### The saved value cannot be used, so the default applies {#invalid-saved}

- **What it means:** a value saved earlier is no longer valid, often after an update changed what the setting accepts.
- **Does Maktaba fix it itself?** It uses the default meanwhile, so nothing breaks.
- **What you can do:** enter a valid value and save, or press **Back to default**.

### The saved rules name settings that no longer exist {#unknown-settings}

- **What it means:** a setting saved earlier was removed or renamed in a newer version.
- **Does Maktaba fix it itself?** It ignores them.
- **What you can do:** press **Remove them** to clean up.

### The preview failed {#preview-failed}

- **What it means:** **Preview channels** could not plan, or "The pipeline does not answer".
- **Does Maktaba fix it itself?** No; your saved rules are not touched.
- **What you can do:** check that processing is running on [Processing](/help/processing), then try again.
