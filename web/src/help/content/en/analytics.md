# Analytics

See how your home is watched, every way it is watched: videos in the Maktaba apps, Live TV in the apps, Plex Live TV, and the remote TV. See who is watching now, the busiest hours, the most watched videos and channels, each person's watching, and how each app uses the server. Only administrators see this page.

## What it is {#what-it-is}

[Administration → Analytics](/admin/analytics) counts every **play** of a video and every **viewing** of a channel, whichever app or device it happens on, and every app's calls to the server. The page has six tabs:

- **Overview**: **Watch time**, **Plays**, **Live TV viewings**, **People watching** and **Live TV hours**; watch time by way of watching and by app; **Watch time over time**; and **Peak viewing hours**, a grid of the days of the week by hour where darker means more watching.
- **Now**: who is watching what right now, the way they watch, the app (and its version), the device, how long they have watched and, for a video, how far they are. It refreshes every 15 seconds.
- **Videos**: **Most watched videos** with their plays, watch time, viewers and **Completion**; the **Libraries** and **Genres** watched.
- **Live TV**: **Most watched channels** with their viewings, people, **Peak** (the most people watching the channel at once) and the ways they were watched; **Peak viewers** across every channel; the **Remote TV** minutes and cost by day.
- **People**: for each person, their watch time, plays, viewings, channels, the apps they used, when they last watched and the last day an app of theirs reached the server.
- **Apps**: for each app, the people using it, **Requests**, **Errors**, **Error rate**, **Slow** requests, **Average time** and the **Versions in use**; the same by platform; **Requests per day**; and the platforms and devices people watch on.

### The ways of watching {#ways}

- **Videos in apps**: a video played in the web or any Maktaba app.
- **Live TV in apps**: a channel watched in the web or a Maktaba app on your home's network.
- **Plex Live TV**: a channel watched in Plex through Maktaba's tuner. Plex does not say who is watching, so these viewings have no person.
- **Remote TV**: a channel watched on the remote TV, away from home.

## How to use it {#how-to-use}

### Change the range {#change-range}

Pick a **Range**: **Today**, **Last 7 days**, **Last 30 days** or **Last 90 days**, or **Custom** and then the **From** and **To** dates. Every tab and the export follow the range. **Now** always shows this moment.

### Filter {#filter}

Narrow everything with **Library**, **Person**, **App** and **Way of watching**. The lists offer the libraries and people seen in the range. A channel is in no library, so choosing a library leaves Live TV out. **Clear filters** shows everything again.

### Export {#export}

Click **Export CSV** or **Export JSON** to download every play and viewing in the range and filters: the kind (play or viewing), who, which video or channel, the way of watching, when it started and ended, how long, how far a play got, how it ended, and the app, version, device and platform.

### Refresh {#refresh}

The numbers can be up to 30 seconds old. Click **Refresh** to load the open tab again.

## What the states and messages mean {#states}

- **Watch time**: the time videos were played and channels were watched in the range.
- **Plays**: how many times a video was played. Watching the same video twice makes two plays.
- **Live TV viewings**: how many times someone watched a channel.
- **People watching**: how many different people watched. Plex Live TV viewers are not counted, as Plex does not say who they are.
- **Completion rate**: the share of plays that reached the end.
- **Unknown app**: an app too old to say which app it is.
- **Nobody is watching right now.**: nothing is playing at the moment.
- **Nothing was watched in this range.** and the other empty messages: there is nothing to show for the range and filters.

## FAQ {#faq}

### Why do I not see someone who just started watching? {#faq-live-delay}

**Now** refreshes every 15 seconds, and a play shows once the player reports in. Wait a moment.

### Why does a person I know watched not appear? {#faq-tracking-off}

Each person can turn off the recording of their watching in their settings. Their plays and viewings are then not recorded at all; the count of requests per app still includes their app, without their name.

### Is Plex's own library counted? {#faq-plex}

No. Only Maktaba's channels watched in Plex (through the tuner) are counted, not what Plex plays from its own library.

### Who can see this page? {#faq-who}

Administrators only. It shows other people's watching, so ordinary users cannot open it.

## When something goes wrong {#troubleshooting}

### An error message above the numbers {#load-failed}

- **What it means:** some numbers could not be loaded; the message gives the reason. The other cards still show theirs.
- **Does Maktaba fix it itself?** No.
- **What you can do:** click **Refresh**. If it keeps failing, check [System health](/help/system-health).

### All the numbers are zero {#all-zero}

- **What it means:** nothing was recorded in the chosen range and filters.
- **Does Maktaba fix it itself?** It fills in as people watch.
- **What you can do:** pick a longer **Range**, or **Clear filters**.

### The Apps tab is empty {#apps-empty}

- **What it means:** no app called the server in the range, or the server has not saved its counts yet (it saves them every minute).
- **Does Maktaba fix it itself?** Yes, within a minute of use.
- **What you can do:** wait a minute and click **Refresh**.

### The export does not download {#export-failed}

- **What it means:** the browser could not get the file, often because your sign-in ended.
- **Does Maktaba fix it itself?** No.
- **What you can do:** reload the page (sign in again if asked) and click the export again.
