# Analytics

See how your library is watched: who is watching now, total watch time, the most watched videos, the busiest hours and the devices people use. Only administrators see this page.

## What it is {#what-it-is}

[Administration → Analytics](/admin/analytics) (**Watch analytics**) counts every time someone plays a video in Maktaba. Each play is a **session**. The page shows:

- **Currently watching**: who is playing what right now, how long they have watched (**Elapsed**), their **Progress** and **Device**. It refreshes every 15 seconds.
- Four headline numbers: **Watch time**, **Sessions**, **Unique viewers** and **Completion rate**.
- **Watch time over time**: a line of watch time across the range.
- **Peak viewing hours**: a grid of the days of the week by hour of the day; darker means more watching.
- **Most watched** and **Most active users**: the top videos and the users who watched most.
- **Popular genres**, **Devices** and **Library usage**: what is watched, on what, and from which library.

## How to use it {#how-to-use}

### Change the range {#change-range}

Pick a **Range**: **Today**, **Last 7 days**, **Last 30 days**, **Last 90 days**, **Last year** or **All time**. Every card updates to that range. **Currently watching** always shows now.

### Export {#export}

Click **Export CSV** or **Export JSON** to download every session in the chosen range: who watched, which video, when it started and ended, how long, how far they got, how it ended, and the device, platform and quality. Open the CSV in a spreadsheet to count it your own way.

### Refresh {#refresh}

The headline numbers can be up to 30 seconds old. Click **Try again** at the top to load everything again.

## What the states and messages mean {#states}

- **Watch time**: the total time videos were played in the range.
- **Sessions**: how many times a video was played.
- **Unique viewers**: how many different users watched.
- **Completion rate**: the share of sessions that were watched to the end.
- **Nobody is watching right now.**: no session is playing at the moment.
- A card with nothing in it: there were no sessions in the range.

## FAQ {#faq}

### Why do I not see a user who just started watching? {#faq-live-delay}

**Currently watching** refreshes every 15 seconds, and a session shows once the player reports in. Wait a moment.

### What counts as a session? {#faq-session}

One play of a video in Maktaba's own player or apps, from start until the viewer stops or closes it. Watching the same video twice makes two sessions.

### Who can see this page? {#faq-who}

Administrators only. It shows other people's viewing, so ordinary users cannot open it.

## When something goes wrong {#troubleshooting}

### An error message under the title {#load-failed}

- **What it means:** the numbers could not be loaded; the message gives the reason.
- **Does Maktaba fix it itself?** No. The cards keep what they last showed.
- **What you can do:** click **Try again**. If it keeps failing, check [System health](/help/system-health).

### All the numbers are zero {#all-zero}

- **What it means:** no session was recorded in the chosen range.
- **Does Maktaba fix it itself?** It fills in as people watch.
- **What you can do:** pick a longer **Range** such as **All time**. Only plays in Maktaba's own player and apps are counted.

### The export does not download {#export-failed}

- **What it means:** the browser could not get the file, often because your sign-in ended.
- **Does Maktaba fix it itself?** No.
- **What you can do:** reload the page (sign in again if asked) and click the export again.
