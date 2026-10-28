# TV changes

Every change to Maktaba TV is prepared next to what is on the air and checked before it goes on the air. A change a person made (the channel editor, the filler pools, the TV settings, a rollback you pressed) waits for an admin's approval; a change Maktaba made itself (the lineup plan, the scheduler, an update's streaming software) goes on the air by itself once it passed its checks. If something goes wrong, you can roll it back.

## What it is {#what-it-is}

When TV staging is on, a change to the channels, a schedule, the TV settings or the channel art does not reach viewers straight away. It becomes a **TV change**:

1. It starts as a **Draft** that collects edits (from the channel editor, the filler pools, the lineup plan, the scheduler, the TV settings or an update).
2. It is **staged**: the new version and its schedules are built next to the live one, without touching it.
3. It is **checked**: Maktaba runs the new version and measures it.
4. A change a person made waits until an admin **approves** it; an automatic one goes on by itself (at once, or in the quiet window if its rule says so), however many channels it touches. It is then **swapped** on the air, each channel at the end of its current programme by default.
5. For a while afterwards the previous version is kept, so you can **roll back**.

Only one change goes through these steps at a time; the others wait their turn. A change never holds up an app update: while the update restarts Maktaba, a change being staged or checked is set aside and checked again on the new version afterwards, and a change waiting for approval keeps waiting (checked again after the update, then back to waiting). You find the changes at **TV changes** ([/admin/live-tv/changes](/admin/live-tv/changes)); the Live TV page shows the change in flight and an **Open Changes** link. Only admins can open these pages. Staging is on by default; it is switched in [Settings → Rules](/settings/rules) under TV staging (see [Rules](/help/rules#tv-staging)).

## How to use it {#how-to-use}

### The changes list {#list}

- At the top: the TV processes (the slot bar) and the change in flight, if any.
- **Show** filters the list: **All**, **Drafts**, **In flight**, **Live**, **Ended**.
- Each row shows the change's number and title, who made it and when, **State**, **Kinds** (what it touches: **Lineup**, **Schedule**, **Settings**, **Art**, **Live broadcasts**, **Version**, **Rollback**), **Source**, **Checks** and **Swap** (when it goes, or went, on the air). Click a title to open it.
- **Staging settings** (click to open) shows the current rules, and **Release history** lists the versions that went on the air, with the live one marked **Live**.

### A change's page {#change-page}

The header shows the state and what is happening now, the kinds, the source, the author, the checks, the swap time and who approved it. Below it are the actions the state allows, and these tabs:

- **Lineup**: channels added, removed, renumbered, renamed, or with definition changes. A draft shows what it holds so far.
- **Schedules**: each channel that gets a new schedule; open one to see the next 24 hours **On the air now** and **After the change**, and where they start to differ.
- **Settings**: each setting, **Now** and **After the change**.
- **Binary & art**: the TV version and the logos, loading pictures and watermarks before and after.
- **Checks**: every check with its result (see [Checks](#checks)).
- **Preview**: watch the staged channels (see [Preview](#preview)).
- **History**: every step of the change, who did it and when, and its audit log.

### The actions {#actions}

- **Stage**: build the draft now (the new version and schedules). **Staging: the release and the new schedules are being built.**
- **Re-verify**: run the checks again.
- **Approve**: approve the change (see [Approve](#approve)). On a draft this approves it ahead.
- **Schedule**: set or change the swap time of a change.
- **Swap now**: put an approved change on the air now.
- **Cancel**: drop the change. Nothing on the air changes. This cannot be undone.
- **Roll back**: go back to the previous version (see [Roll back](#rollback)).

### Checks {#checks}

While the change is **Verifying**, Maktaba runs the staged version and checks it: the staging process, the lineup, guide coverage, that the programme on the air is kept, the first segment and segment cadence, the wall clock, the TS and HLS streams, a seamless switch, alignment with live, Cloudflare ingest and remote pushes, that the guide matches the stream, the encoders and a soak test. Each check has one result:

- **pass**: fine.
- **warn**: works, but something deserves a look (for example a channel with no logo, or a guide that does not reach far enough). A change with warnings is never put on the air automatically: an admin approves it.
- **fail**: the change is not safe to air. It moves to **Failed**.
- **skipped**: the check did not apply.

The **Checks** column and tab count them ("3 pass, 1 warn, 0 fail"). **Why it failed:** lists the reasons. When only one TV process runs, the checks run on the database only (lineup, guide, the programme on the air) and the page says so.

### Preview {#preview}

On the **Preview** tab you can watch the staged channels as the staging process airs them, before anyone else sees them, side by side with **Live**. Only admins can open these streams; viewers, Plex and Cloudflare stay on the live channels. A change can be previewed while it is **Verifying**, **Ready** or **Approved**. The tab also gives the staged lineup (M3U) and guide (XMLTV) for VLC or another player; the links work until the time shown.

### Approve {#approve}

1. Open the change and click **Approve**.
2. Under **When**, choose **As soon as possible (each channel at its next programme boundary)** or **At a set time** and enter **Swap at**.
3. Under **How channels switch**, choose **each channel switches at its next programme boundary** or **every channel switches at once**. **Expected switch times** shows, for each changed channel, what is on the air then and when it switches.
4. If the change alters the picture of a channel that is sent on (remote viewing or simulcast), tick **I understand the picture of a pushed channel changes**.
5. Click **Approve**. Until the swap time nothing on the air changes.

If **A second admin approves** is on, you cannot approve a change you made; another admin must. You can approve a draft ahead: the approval carries over when its checks pass. If the draft is edited after that, the approval is withdrawn.

### Swap time {#swap-time}

**Schedule** changes when an approved change goes on the air, with the same choices as **Approve**. **Swap now** puts an approved change on the air at once; the dialog shows how watched channels will switch.

Changes can also go on the air by a rule instead of an approval: **an admin approves**, **in the quiet window**, or **at once** after the checks pass with no warnings. The rule for each kind of change is under **Goes on the air when** in **Staging settings**.

### Roll back {#rollback}

On a **Live** change (or one **Swapping**), **Roll back** returns to the previous version. The hint next to the button says which kind you get:

- **Warm (instant)**: while the previous version is still kept warm (15 minutes by default). Every channel switches back now, at a keyframe; viewers, Plex tuners, the Cloudflare broadcast and its simulcast outputs keep playing. The guide and Plex's channel map follow.
- **Cold (~3 min)**: after the warm period, while the previous version is still kept (24 hours by default). The previous version is staged again as a new change, checked, and swapped at each channel's programme boundary. Your request approves it.
- No rollback point: the button is off, and its hint says why.

With **Roll back automatically** on (the default), Maktaba rolls back by itself after a crash or a failed check just after a switch.

### Hot standby {#hot-standby}

With **Hot standby** on (the default), a second copy of what is on the air stays ready and takes over at once if the live TV process crashes. You get the notice "Live TV failed over to the hot standby". The slot bar shows the TV processes and their role: **LIVE**, **STANDBY**, **STAGING** and **PREVIOUS**.

### Settings {#settings}

All the staging rules (switching, quiet window, second admin, how long the previous version is kept, rules per kind, hot standby, automatic rollback) are in [Settings → Rules](/settings/rules) under TV staging. See [Rules](/help/rules#tv-staging).

## What the states and messages mean {#states}

### Change states {#change-states}

- **Draft**: being edited. Saves from the editors collect here. It waits for **Stage** (or its producer) to move on; "waiting for the change in flight" means it is queued behind another change.
- **Staged**: built; the checks start.
- **Verifying**: the checks are running ("checking: n done, n failed").
- **Ready**: checked; waiting for an approval, or for its rule.
- **Approved**: approved; going on the air now, or waiting for its swap time.
- **Swapping**: the channels are switching to the new version.
- **Live**: on the air. It stays the live change until a newer one goes live, or until you roll it back.
- **Superseded**: a newer change took its place.
- **Failed**: at least one check failed. It never goes on the air. Fix the cause and make your edits again: your next save starts a new draft.
- **Cancelled**: an admin dropped it. Nothing on the air changed.
- **Rolled back**: it was on the air and was rolled back to the previous version.

### TV processes {#slots}

On the slot bar each TV process shows its role and state: **healthy**, **starting**, **warming**, **draining**, **stopped** or **crashed**. "One TV process runs today" means the staging and standby processes are not running yet, so checks are database-only.

### Notifications {#notifications}

You get a notification when a change passed its checks and needs your approval, was put on the air by its rule, failed its checks, could not go on the air and is back to **Ready**, is on the air, was rolled back, or has waited hours for an approval, and when the rollback point is about to end.

## FAQ {#faq}

### I saved a channel but nothing changed on the TV. Why? {#faq-not-live}

Your edit is in a draft change. Open it from the channel editor's **Open change #n**, or from the **Drafts** filter here, then **Stage**, check and **Approve** it.

### Can I approve my own change? {#faq-own}

Yes, unless **A second admin approves** is on. Then another admin must approve it.

### When exactly do viewers see the change? {#faq-when}

At the swap time you choose. By default each watched channel switches at the end of the programme on the air, waiting at most the longest wait set in the staging rules. **Expected switch times** in the approve dialog shows the times.

### How long can I roll back? {#faq-rollback-window}

Instantly while the previous version is kept warm (15 minutes by default), then in about 3 minutes while it is kept (24 hours by default). After that there is no rollback point.

### Why is my change waiting and not staging? {#faq-queued}

Only one change is in flight at a time. Yours starts when the current one is live, failed or cancelled.

### Can I turn staging off? {#faq-off}

Yes, in [Settings → Rules](/settings/rules) under TV staging. Changes then go on the air at once, and the release history stays on this page.

## When something goes wrong {#troubleshooting}

### A second admin approves this change: you made it {#second-admin}

- **What it means:** the second-admin rule is on and you are the change's author.
- **Does Maktaba fix it itself?** No. The change waits in **Ready**.
- **What you can do:** ask another admin to approve it.

### This change changes the picture of a pushed channel: acknowledge it first {#ack-picture}

- **What it means:** a channel sent on for remote viewing or simulcast will look different, and remote viewers may reconnect.
- **Does Maktaba fix it itself?** No.
- **What you can do:** tick **I understand the picture of a pushed channel changes** in the dialog, then approve.

### Another change is in flight; this one waits for its turn {#in-flight}

- **What it means:** only one change goes through staging at a time.
- **Does Maktaba fix it itself?** Yes. Yours starts when the current one ends.
- **What you can do:** wait, or finish the change in flight (approve or cancel it).

### Not possible now: the change is … {#not-possible-now}

- **What it means:** the action does not fit the change's state any more, often because it moved on while you looked.
- **Does Maktaba fix it itself?** The page refreshes by itself every few seconds.
- **What you can do:** look at the new state and use the actions it offers.

### Checks failed {#checks-failed}

- **What it means:** at least one check failed; the change is **Failed** and will not go on the air.
- **Does Maktaba fix it itself?** No. Nothing on the air changed.
- **What you can do:** open **Checks**, read **Why it failed:** and the failed rows, fix the cause and make your edits again in the editor. Your next save starts a new draft.

### Checks with warnings {#checks-warn}

- **What it means:** the change works, but a check found something worth a look.
- **Does Maktaba fix it itself?** No. A change with warnings never goes on the air by a rule; it waits for an admin.
- **What you can do:** read the warnings on **Checks**, then approve if they are fine.

### The checks could not run {#checks-error}

- **What it means:** the checks themselves failed to run; the message says why.
- **Does Maktaba fix it itself?** No.
- **What you can do:** click **Re-verify**. If it keeps failing, check [System health](/help/system-health).

### Could not go on the air and is back to ready {#swap-failed}

- **What it means:** the swap failed, so the change went back to **Ready**. The live version kept playing.
- **Does Maktaba fix it itself?** No. It waits for an admin instead of retrying again and again.
- **What you can do:** approve it again or click **Swap now**. If it fails again, cancel it and look at **History**.

### Was not started within … min and is being rolled back {#swap-not-taken}

- **What it means:** the swap was not taken up in time (the TV service was not running), so it is rolled back.
- **Does Maktaba fix it itself?** Yes, it rolls the change back.
- **What you can do:** make sure TV is running (see [System health](/help/system-health)), then make the change again.

### Rolled back automatically {#rolled-back}

- **What it means:** a crash or a failed check just after the switch made Maktaba go back to the previous version; the notification says the cause.
- **Does Maktaba fix it itself?** Yes, that was the fix: the previous version is on the air.
- **What you can do:** open the change's **History** and **Checks**, fix the cause, and make the change again.

### Rolled back in the database only {#rollback-degraded}

- **What it means:** no TV process was live, so the rollback was recorded but the channels on the air were not switched back.
- **Does Maktaba fix it itself?** Yes: the channels switch back when the TV processes restart.
- **What you can do:** check that TV is running on [System health](/help/system-health).

### The rollback could not be committed {#rollback-not-committed}

- **What it means:** the rollback failed to save; the message gives the error.
- **Does Maktaba fix it itself?** No.
- **What you can do:** try **Roll back** again. If it keeps failing, check [System health](/help/system-health).

### No rollback point {#no-rollback-point}

- **What it means:** there is nothing to go back to: nothing went on the air before this, the previous version is no longer kept, or the swap is still being saved.
- **Does Maktaba fix it itself?** If the swap is still being saved, the button comes back in a moment.
- **What you can do:** wait a moment and try again; otherwise make a new change that undoes what you need.

### The staging slot cannot play this channel now {#preview-unavailable}

- **What it means:** the preview has no encoder free, often because they are all in use.
- **Does Maktaba fix it itself?** Yes, it retries.
- **What you can do:** wait, or try again later.

### The TV staging process keeps crashing {#staging-crash}

- **What it means:** the process that runs staged changes keeps stopping.
- **Does Maktaba fix it itself?** No. Live channels keep playing.
- **What you can do:** check [System health](/help/system-health) and the logs; cancel the change in flight if it causes the crash.

### TV staging is off {#staging-off}

- **What it means:** changes go on the air at once, without checks or approval.
- **Does Maktaba fix it itself?** No; this is a setting.
- **What you can do:** to stage changes again, switch on **Stage every TV change** in [Settings → Rules](/settings/rules) under TV staging.
