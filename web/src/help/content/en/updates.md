# Updates

How Maktaba finds new releases and installs them, and how to control when.

## What it is {#what-it-is}

Maktaba checks for new releases by itself. Every release is signed, and Maktaba checks the signature before it installs anything. With automatic updates on, a new release installs by itself in the quiet window you choose: first on the home (the main machine), then on every other machine one by one, each checked before the next. If a new version cannot start, Maktaba puts the previous one back.

Admins see everything about updates on one page, [Admin → Updates](/admin/updates). [Settings → About](/settings/about) says it in one line (**up to date**, **update available**, **updating** or **behind**) with a link there. A dot on **Settings** (or **More** on a phone) means an update is available.

## How to use it {#how-to-use}

### See your version and check now {#check-now}

Open [Admin → Updates](/admin/updates). The **This home** card shows the version the home **Running**, its **Last update** (the version, when, and how: by an update run, by the desktop app, or installed by hand), what happens **Next**, and whether **Automatic updates** are on. Press **Check now** at the top to look for a new release right away.

A run that failed or was rolled back is never the last update: it shows under **Earlier updates**.

When a release is available, its card shows **Release …**, its **Release notes**, a **View release** link and **What happens**: the backup of the library database (only when the release changes it), the home's restart, then each machine one at a time.

Everyone else sees the version on [Settings → About](/settings/about), with one line saying **up to date** or **update available**.

### Update now {#update-now}

Admins press **Update now** on the release's card, and confirm. It does not wait for the quiet window or the release age. The update shows step by step on the page while it runs.

The one-container Docker install updates itself like any other. A container of the stack built from source cannot: the release's card shows **This is a Docker install. Run:** and the command to run on the server instead.

### Automatic updates {#automatic-updates}

**Pause automatic updates** (and **Resume automatic updates**) is on the **This home** card. The rest is under **Automatic updates** on [Admin → Updates](/admin/updates); open it to change:

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

### The Updates page {#updates-page}

Admins open [Admin → Updates](/admin/updates). It shows, in this order:

- **This home**: its version, last update, what happens next and automatic updates.
- **Installed. TV held on …**: an update installed while its TV change failed its check; the TV stays on the previous version until you press **Retry** or **Drop** ([more](/help/updates#tv-held)). The home's row in **Machines** says "TV held on …" meanwhile.
- **Release …**: the release available, and what will happen when it installs, with **Update now** and **Skip this version**.
- **The update under way**: the version being installed and its step on the home and on each machine. **Cancel this update** stops it before the home switches. On a machine's row: **Retry**, **Skip** and **Roll back**. **Update the held machines** goes on with a rollout a failure held.
- **Machines**: each machine's **Version** (the one it runs), its **State** (**up to date**, **update available**, **updating**, **behind** or **offline**), its **Last update** and what happens **Next**: for a machine behind the home, when it catches up (now, in the next quiet window, after a retry wait, or why not). Under it, its last attempt: when, from which version to which, the step it stopped at, the reason in plain words with **Learn more**, what to do next, **See the logs** and **Download the logs**.
- **Earlier updates**, **Automatic updates** and **Backups of the library database**: folded; open them to see the past runs, change the settings, or **Restore** a backup.

### Put a backup back {#restore-backup}

**Restore** puts the home back as it was when the backup was taken. Type the backup's name to confirm. Maktaba restarts on the version that matches the backup and puts it back before anything else runs; the database it replaces is kept as a backup too. **Every change made since the backup was taken is lost.** It is refused while an update runs, or when no kept version matches the backup (it names the version to install first). The other machines copy the restored database by themselves.

## What the states and messages mean {#states}

### On Settings → About {#about-messages}

- **up to date**: no newer release on your channel; on a joined machine, it runs the home's release.
- **update available …**: a newer release is ready. Admins open **Open Updates**.
- **updating …**: an update is under way.
- **behind …**: this machine runs an older release than the home; it catches up by itself (see its **Next** on [Admin → Updates](/admin/updates)).
- **Automatic update checks are off.**: Maktaba does not check for releases on this server.
- **No successful update check for 14 days. Check the network or GitHub access.**: shown on Admin → Updates; see below.

### What happens next {#plan-messages}

- **Nothing to install: up to date.**: nothing to install.
- **… installs tomorrow between 03:00 and 05:00 (in 9 hours).**: the day, the quiet window, and how long until then.
- **Version … installs in the next quiet window.**: it waits for the window.
- **Version … installs once it is old enough.**: it waits for the release age.
- **Version … is deferred.**: it waits for the date you set in **No automatic update before**.
- **Version … is available; automatic install is off.**: install it with **Update now**, or turn automatic updates on.
- **Updating to … now.**: an update is under way; its steps show below.
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
- **… is installed, but the TV stays on …: its check failed**: see [The TV stayed on the previous version](/help/updates#tv-held).

## FAQ {#faq}

### Will an update interrupt what people are watching? {#faq-interrupt}

Automatic updates start only in the quiet window. On most servers the TV keeps playing across the restart and moves to the new version at a programme boundary; on other installs the update waits until nobody watches. Running jobs are given time to finish, and transcriptions resume from where they were.

### How do I stay on the version I have? {#faq-stay}

Turn off **Install updates automatically**, or set **Stay at or below version** to your current version.

### Can I go back to an older version? {#faq-downgrade}

Maktaba goes back by itself when a new version fails. To go back further, [put a backup back](/help/updates#restore-backup) on Admin → Updates: changes made since that backup are lost.

### Why is there a dot on Settings? {#faq-dot}

An update is available. Admins open [Admin → Updates](/admin/updates) to see it.

## When something goes wrong {#troubleshooting}

### Update failed and was rolled back. {#rolled-back}

- **What it means:** The new version was installed but did not start properly, so Maktaba put the previous version back.
- **Does Maktaba fix it itself?** Yes, it already restored the version that worked.
- **What you can do:** Read the reason on [Admin → Updates](/admin/updates). Check [System health](/help/system-health) and the [Logs](/help/logs). Try **Update now** again later, or wait for the next release.

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
- **What you can do:** Check [System health](/help/system-health) and the [Logs](/help/logs), then press **Update now** on [Admin → Updates](/admin/updates) to try again.

### The library database could not be updated {#code-database}

- **What it means:** The step that saves the library database and brings it up to date for the new version failed, or the changes failed when tried on a copy first, or the new version needs another database version than this machine has.
- **Does Maktaba fix it itself?** In most cases: nothing was changed, or the database was put back as it was before the update and the previous version runs. That version is not installed again by itself.
- **What you can do:** Read the reason and **See the logs**. When it says the database could not be put back, or the version is older than the database, [put a backup back](/help/updates#restore-backup).

### The TV during an update {#code-tv}

- **What it means:** The TV moves to the new version at a programme boundary. Someone watched until the quiet window ended, another TV change waited for your approval, or the TV change's swap failed, the TV failed on the air after it switched, it took too long or it was cancelled.
- **Does Maktaba fix it itself?** Yes: it waits for the next quiet window, or puts the previous version back. A swap that failed blocks that version until you try it again.
- **What you can do:** Approve or reject the change waiting on [TV changes](/admin/live-tv/changes). Press **Try it again** once the cause is fixed. A change that only failed its check does not undo the update: see [The TV stayed on the previous version](/help/updates#tv-held).

### The TV stayed on the previous version {#tv-held}

- **What it means:** The update installed: the new server, web app and pipeline run. Only the TV's check of the new streaming software failed, before the TV switched, so the TV keeps playing on the previous version's streaming software. **Admin → Updates** says "Installed. TV held on …: its check failed", with what failed.
- **Does Maktaba fix it itself?** No: nothing is undone and the version is not blocked, but the TV stays where it is until you choose.
- **What you can do:** Press **Retry** to check the TV change again: when it passes, the TV moves at a programme boundary as usual. Press **Drop** to withdraw it: the TV stays on the previous version until a later release moves it. A later release replaces a held change by itself. Only a swap that fails, or a TV that fails on the air after it switched, puts the whole previous version back.

### A machine did not follow {#code-machine}

- **What it means:** A machine could not get the new version ready, could not install it, or was not healthy in time. Other machines of the same kind may wait for it.
- **Does Maktaba fix it itself?** Yes: the machine goes back to its previous version and takes no work until you act. A machine that was offline is updated when it comes back.
- **What you can do:** On [Admin → Updates](/admin/updates), read its reason and logs, then press **Retry**, **Skip**, or **Update the held machines**.

### The desktop app could not install the update {#code-desktop-app}

- **What it means:** The Maktaba app on that machine could not replace itself: its folder needs an administrator, macOS blocks it, the download failed, or the app did not come back on the new version.
- **Does Maktaba fix it itself?** It keeps the version that runs, and tries again later when that can help.
- **What you can do:** Follow the fix shown under the reason: it names the setting or the command. An app built on the machine itself cannot update: install the release by hand once.

### The processing environment could not be set up {#code-python}

- **What it means:** The new version's processing environment could not be built: the machine has no suitable Python 3.12, or its packages could not be downloaded.
- **Does Maktaba fix it itself?** It keeps the previous version and tries again at the next start or quiet window.
- **What you can do:** Install Python 3.12 on that machine, or check its network. On Windows, turn off the Microsoft Store's Python alias.

### This machine cannot update itself as it is installed {#code-needs-person}

- **What it means:** The machine was installed in a way that cannot take a new version by itself (a program copied by hand, a launcher too old, a container of the stack built from source), or the version needs a newer launcher from its package manager.
- **Does Maktaba fix it itself?** No. The other machines update as usual.
- **What you can do:** Follow the fix under the reason: usually one command, or one version installed by hand. A machine that shows **Worker only — re-run the installer once** needs its installer run once.

### The release could not be downloaded {#code-download}

- **What it means:** A file of the new version could not be downloaded, is missing for this machine, or there was no room on the disk.
- **Does Maktaba fix it itself?** Yes: it tries again at once, then later, then in the next quiet window. Machines download from the home first, then from GitHub.
- **What you can do:** Check the network and the free disk space, then press **Retry**, or wait.

### Write the window as HH:MM-HH:MM {#bad-window}

- **What it means:** The **Quiet window** is not written correctly, or it is 30 minutes or shorter.
- **Does Maktaba fix it itself?** No, it will not save.
- **What you can do:** Write it like 03:00-05:00. For a window over midnight, write 23:00-01:00.

### A pin cannot be below the installed version {#pin-below}

- **What it means:** **Stay at or below version** is lower than the version you have, or not written like 1.5.9.
- **Does Maktaba fix it itself?** No, it will not save.
- **What you can do:** Use your current version or a higher one, or leave the box empty.
