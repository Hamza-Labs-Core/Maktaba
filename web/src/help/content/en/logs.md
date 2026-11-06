# Logs

Read what Maktaba is doing line by line, find errors and download a diagnostics bundle for a bug report. Only administrators see these pages.

## What it is {#what-it-is}

Maktaba writes a log: one line for each thing it does, with the time, the level (**Debug**, **Info**, **Warning**, **Error**), the service and a message. You can read it in two places.

- **[Administration → Logs](/admin/logs)**: the most recent lines of this server, refreshed every few seconds. This is also where you export the diagnostics bundle.
- **[Machines](/admin/machines) → Logs tab**: the log of any machine or process in the cluster, live or for an earlier day. See [Machines](/help/machines).

## How to use it {#how-to-use}

### Find errors {#filter-errors}

1. Open [Administration → Logs](/admin/logs).
2. Set **Level** to **Error**. The level shows that level and everything more serious, so **Warning** shows warnings and errors.
3. Pick a **Service** to see one part only, or keep **All services**.
4. Type in **Search** to keep only lines that contain some text, for example a video's name.

Click the small arrow at the end of a line to see all of its details.

### Search, and follow one request {#search}

Type words in **Search logs** and press Enter. Put a phrase in quotes ("connection refused"), put a minus before a word to leave it out (-disk), or end a word with \* to find what starts with it. Pick a **Level**, a **Service** and a **Time** to narrow the list; they stay in the page's address, so a link keeps them.

When an error screen in an app shows **Reference: xxxxxxxx**, type those 8 characters in **Search logs**. **Follow this request** opens: every line of that one action, from every part of Maktaba, in time order, with a dashed line where more than a second passed. A line's **Request** chip opens the same view, and **Lines around this** (under a line's details) shows what every part wrote at that moment.

### Watch it live {#live}

Tick **Live** to see lines as they are written, newest at the top, within about two seconds. Scroll down to read and the list holds still; **N new lines** takes you back to the top with what arrived.

### Errors {#errors}

[Administration → Errors](/admin/errors) groups the errors that look alike: one row per problem, with how often it happened today and in all, a 14-day chart, how many people it hit and when it was last seen. The number on **Errors** in the menu is how many are **New**. Open one to see its recent lines and act on it: **Mark seen**, **Resolve** (it comes back as new, marked **Came back**, if it happens again in a later version) or **Mute** it for 24 hours, 7 days or until it is resolved. Administrators get a notification for a new error that happens again and again, or one that came back.

### Keep diagnostics, and detailed logs for an hour {#retention}

[Diagnostics settings](/admin/logs/settings) says how long Maktaba keeps its logs (7 days at first), its errors (30 days) and problem reports (90 days), and how much disk they may use in all (1 GB at first), with what each uses now. When the limit is reached the oldest go first, even if they are newer than the days you chose.

**Detailed logs for one hour** makes one part (the API, streaming, processing or log search) write its debug lines for an hour, then go back by itself. Use it when you are asked for more detail about a problem.

### Dashboards {#dashboards}

[Administration → Dashboards](/admin/dashboards) draws how the home has been doing: **Overview** (components up, restarts, errors per hour, the API's memory and CPU), **Queue**, **Processing**, **Streaming**, **Storage** (backups and the log files) and **Use** (plays and people per day, the most played videos; counts only, never one person's history). Pick a time from 1 hour to 90 days; point at a chart to read its values, and **Open in Logs** shows the lines of that time. Maktaba keeps a minute's detail for 7 days and an hour's for 30.

### Stop the scrolling {#pause}

New lines keep arriving. Click **Pause** to freeze the list while you read, and **Resume** to carry on. Untick **Auto-scroll** to stay where you are while lines keep coming.

### Export a bundle for a bug report {#export}

Click **Export bundle**. Your browser downloads `maktaba-diagnostics.tar.gz`. It holds the recent logs of the API, Live TV and streaming, and the processing pipeline, plus a summary of recent errors, a health snapshot and the server's version and system details. The **Level** and **Service** you chose apply to it. Attach the file to your report.

### Read a machine's or process's log {#machine-logs}

1. Open [Machines](/admin/machines) and the **Logs** tab, or click **Machine logs** on a machine under **Running now**.
2. In **Show logs of**, choose a whole machine or one process.
3. Choose **Live** to follow it as it happens (**Follow**, **Pause**, **Clear**), or **Earlier days** to read a past **Day** (**Load older** for more).
4. Use **Level** and **Search** as above.

Below the viewer, every machine is listed with its processes and the days on record; click a day to open it.

## What the states and messages mean {#states}

- **Debug**: fine detail, useful when tracking down a problem.
- **Info**: normal activity.
- **Warning**: something unusual that Maktaba worked around.
- **Error**: something failed. Start here.
- **No log entries match the current filters.**: nothing matches; widen **Level** or clear **Search**.
- **Paused; N new lines waiting.** (Machines → Logs): the view is paused and N lines arrived meanwhile.
- **N lines, live.** / **N lines from …**: what the machine viewer shows now.
- **Diagnostics bundle downloaded.**: the export finished.

## FAQ {#faq}

### How far back does the Logs page go? {#faq-how-far}

It shows the most recent lines the server keeps in memory, not the whole history, and they start again when the server restarts. For earlier days use Machines → **Logs** → **Earlier days**.

### Is the bundle safe to share? {#faq-share}

It holds log lines, which can include file names, user names and addresses on your network. Share it only with the people helping you.

### Can a non-administrator export logs? {#faq-users}

No. These pages are for administrators.

## When something goes wrong {#troubleshooting}

### Could not load logs {#load-failed}

- **What it means:** the page could not fetch the lines from the server.
- **Does Maktaba fix it itself?** Yes. It tries again every few seconds while not paused.
- **What you can do:** wait a moment. If it stays, check [System health](/help/system-health).

### Export failed {#export-failed}

- **What it means:** the bundle could not be built or downloaded. The reason follows the message.
- **Does Maktaba fix it itself?** No.
- **What you can do:** click **Export bundle** again. If a part of Maktaba is down, the bundle may fail until it is back.

### The log could not be read (Machines → Logs) {#machine-log-failed}

- **What it means:** the machine's live log or its saved day could not be read.
- **Does Maktaba fix it itself?** The live view reconnects by itself.
- **What you can do:** check that the machine is **Online** on the Machines tab, then choose it again. Saved days need the shared storage to be reachable.
