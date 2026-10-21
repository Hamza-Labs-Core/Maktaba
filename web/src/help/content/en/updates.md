# Updates

How Maktaba finds new releases and installs them, and how to control when.

## What it is {#what-it-is}

Maktaba checks for new releases by itself. Every release is signed, and Maktaba checks the signature before it installs anything. With automatic updates on, a new release installs by itself in the quiet window you choose: first on the home (the main machine), then on every other machine one by one, each checked before the next. If a new version cannot start, Maktaba puts the previous one back.

You see the version and any update on [Settings → About](/settings/about). A dot on **Settings** (or **More** on a phone) means an update is available.

## How to use it {#how-to-use}

### See your version and check now {#check-now}

Open [Settings → About](/settings/about). You see the **Current version**, the **Update channel**, and when Maktaba **Last checked**. Press **Check now** to look for a new release right away.

When there is one, **Update available: …** shows with its **Release notes** and a **View release** link.

### Update now {#update-now}

Admins see **Update now** under an available update.

1. Press **Update now**.
2. The page shows **Downloading…**, then **Restarting…** while Maktaba restarts.
3. When it is back you see **Updated. Now running …**.

**Update now** does not wait for the quiet window or the release age.

On a Docker install there is no button. The page shows **This is a Docker install. Run:** and the command to run on the server instead.

### Automatic updates {#automatic-updates}

Admins see **Automatic updates** at the bottom of [Settings → About](/settings/about). The first line says what happens next, for example **Version … installs on …**.

- **Install updates automatically (home first, then every machine)**: off means Maktaba only checks and tells you.
- **Release channel**: **Stable** gets releases only; **Beta** also gets pre-releases. Going back to **Stable** never installs a lower version.
- **Quiet window**: when updates may start, in the home's local time, written as HH:MM-HH:MM. 23:00-01:00 crosses midnight. An update starts only inside it, at least 30 minutes before its end. The default is 03:00-05:00.
- **Release age before installing (hours)**: how old a release must be before it installs by itself. The default is 24. If the release is pulled in that time, it is never installed.
- **Stay at or below version**: a pin, for example 1.5.9. Maktaba never goes above it. Empty means no pin. It cannot be lower than the version you have.

Press **Save update settings**. You see **Update settings saved.**

### Defer or skip a release {#defer-skip}

- **Defer**: in [Settings → Rules](/settings/rules), section **Updates**, set **No automatic update before** to a date and time. Nothing installs by itself before then. Clear it to stop deferring.
- **Skip**: add a version to **Skipped versions** in the same section. It is never installed automatically; a newer one is.

The same section holds the rollout choices, such as **When a machine fails** and **A machine that was offline is updated**. See [Rules → Updates](/help/rules#updates).

### A release that needs your approval {#held-release}

Some releases change the database in a way that cannot be undone by going back to the previous version. Maktaba never installs those by itself. It tells you **Maktaba … needs your approval before it installs**, and the [TV changes](/admin/live-tv/changes) page shows **Version … needs your approval** with two buttons:

- **Approve and update now**: install it now.
- **Skip this version**: never install it automatically.

## What the states and messages mean {#states}

### On Settings → About {#about-messages}

- **You're up to date.**: no newer release on your channel.
- **Automatic update checks are off.**: Maktaba does not check for releases on this server.
- **Update failed.**: the update could not run. Press **Update now** to try again.
- **Update failed and was rolled back.**: the new version did not start, so the previous one was put back. You are still on your old version.
- **The last update, to …, could not start and was rolled back.**: the same, reported after the fact, with the reason.
- **Automatic install arrives with the update orchestrator.**: this server can check for updates but cannot install them by itself yet.
- **No successful update check for 14 days. Check the network or GitHub access.**: see below.

### What happens next {#plan-messages}

- **No update planned: you are up to date.**: nothing to install.
- **Version … installs on ….**: the time it will start.
- **Version … installs in the next quiet window.**: it waits for the window.
- **Version … installs once it is old enough.**: it waits for the release age.
- **Version … is deferred.**: it waits for the date you set in **No automatic update before**.
- **Version … is available; automatic install is off.**: install it with **Update now**, or turn automatic updates on.
- **Version … waits for your approval (Live TV -> Changes).**: see [A release that needs your approval](/help/updates#held-release).
- **Version … cannot be installed from this version.**: you need a newer version first before this one can install.

### Notifications {#update-notifications}

- **Maktaba … is available**: a new release was found.
- **Maktaba … installs at …**: an automatic update is planned.
- **Maktaba … needs your approval before it installs**: see [A release that needs your approval](/help/updates#held-release).
- **Release … failed its signature check (…) and will not be installed**: see below.
- **Release … was withdrawn (…) and will not be installed**: the release was pulled after it came out.
- **Release … was re-published with other files and was refused**: see below.
- **No successful update check for … days**: see below.
- **The update to … could not start (…)**: see below.

## FAQ {#faq}

### Will an update interrupt what people are watching? {#faq-interrupt}

Automatic updates start only in the quiet window, so choose hours when nobody watches. Running jobs are given time to finish, and transcriptions resume from where they were.

### How do I stay on the version I have? {#faq-stay}

Turn off **Install updates automatically**, or set **Stay at or below version** to your current version.

### Can I go back to an older version? {#faq-downgrade}

Not from the app. Maktaba only goes back by itself when a new version fails to start.

### Why is there a dot on Settings? {#faq-dot}

An update is available. Open [Settings → About](/settings/about) to see it.

## When something goes wrong {#troubleshooting}

### Update failed and was rolled back. {#rolled-back}

- **What it means:** The new version was installed but did not start properly, so Maktaba put the previous version back.
- **Does Maktaba fix it itself?** Yes, it already restored the version that worked.
- **What you can do:** Read the reason on [Settings → About](/settings/about). Check [System health](/help/system-health) and the [Logs](/help/logs). Try **Update now** again later, or wait for the next release.

### Release … failed its signature check {#verify-failed}

- **What it means:** The downloaded release is not signed correctly. It may be damaged or not genuine.
- **Does Maktaba fix it itself?** Yes: it refuses to install it.
- **What you can do:** Nothing is needed. If it repeats for every release, check the server's network and time.

### Release … was re-published with other files and was refused {#replay}

- **What it means:** A version you already have was published again with different files. Maktaba refuses it for safety.
- **Does Maktaba fix it itself?** Yes: it keeps what you have.
- **What you can do:** Nothing. Wait for the next release.

### No successful update check for 14 days {#check-stale}

- **What it means:** Maktaba could not reach the release site (GitHub) for two weeks.
- **Does Maktaba fix it itself?** It keeps trying.
- **What you can do:** Check that the server has internet access and can reach GitHub. Press **Check now** to try again.

### The update could not start {#run-failed}

- **What it means:** The part of Maktaba that installs updates did not accept the update. The code in brackets says why.
- **Does Maktaba fix it itself?** No. Nothing was changed: you are still on your current version.
- **What you can do:** Check [System health](/help/system-health) and the [Logs](/help/logs), then press **Update now** on [Settings → About](/settings/about) to try again.

### Write the window as HH:MM-HH:MM {#bad-window}

- **What it means:** The **Quiet window** is not written correctly, or it is 30 minutes or shorter.
- **Does Maktaba fix it itself?** No, it will not save.
- **What you can do:** Write it like 03:00-05:00. For a window over midnight, write 23:00-01:00.

### A pin cannot be below the installed version {#pin-below}

- **What it means:** **Stay at or below version** is lower than the version you have, or not written like 1.5.9.
- **Does Maktaba fix it itself?** No, it will not save.
- **What you can do:** Use your current version or a higher one, or leave the box empty.
