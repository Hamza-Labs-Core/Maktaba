# Recovery

See what went wrong and fix it when part of Maktaba is not working, even when the database or the Maktaba service itself is down. Only administrators can open it.

## What it is {#what-it-is}

The recovery page lives at `/recovery` on your home's web address. Maktaba's supervisor, the program that starts and watches every other part, serves it itself, so it works while the database or the Maktaba service is down.

It shows each part (database, Maktaba service, playback, processing, search, log search) and the components inside it, with their restarts, when each last stopped and its last error. It also shows the warnings and errors of the last 15 minutes, read straight from the log files on disk, and how much disk space is free.

## How to use it {#how-to-use}

### Open it {#open}

- **On the Mac that runs your home:** choose **Open recovery page** in the Maktaba menu. The page opens already signed in, with a link that works once, for two minutes.
- **From any browser:** open `/recovery` on your home's address. If you signed in as an administrator in this browser during the last 7 days, you are let in. The **Maktaba is restarting** page also offers **Open the recovery page** once the service has been down for a minute.

### Fix a problem {#fix}

1. Read the parts at the top: anything not **working** is listed with its state.
2. Look at the component's last error, then at **Recent problems** below. Type in the search box to find a line, or **Load 15 more minutes**.
3. Choose an action. Each one asks you to confirm.
   - **Restart** a component, or **Restart everything**. This only restarts it: it does not turn anything on or off.
   - **Start in safe mode**: Maktaba starts once with only the database, the Maktaba service, updates and log search. Playback, processing and Live TV stay off until the next restart. **Leave safe mode** goes back to normal; **Stay in safe mode** keeps it for one more start.
   - **Go back to version …**: only when an update can be rolled back.
   - **Restore a backup**: replaces the database with a backup on the data disk. It asks twice.
   - **Download diagnostics**: the last two hours of logs and the state of every part, with private details removed.

Every action is recorded, and copied to the [Audit log](/help/audit-log) when the Maktaba service is back.

## What the states and messages mean {#states}

- **Working**: it runs and answers its checks.
- **Starting**: it has not been checked yet.
- **Having trouble**: it runs but fails its checks, or one of several workers stopped.
- **Restarting**: it stopped and is about to start again.
- **Stopped**: it kept crashing and was cut off. It stays stopped until you restart it here or the machine restarts.
- **Off**: you turned it off in [Components](/help/components), or it is waiting for something it needs. Off is not a problem.
- **Maktaba can't start**: Maktaba itself stopped three times in a row before it was ready. That page offers **Try again**, **Start in safe mode** and, when one is kept, **Go back to version …**. The Maktaba menu on the Mac offers the same choices.

## FAQ {#faq}

**Why can't I sign in?**
Only administrators can open the page. Use the Maktaba menu on the Mac, or sign in to Maktaba as an administrator when it answers again, then come back.

**Does safe mode change my settings?**
No. It lasts one start and changes nothing you set: the next normal start runs everything as before.

**Can I turn a component off here?**
No. Turning components on and off is in [Components](/help/components), which needs the Maktaba service. A component that keeps crashing is stopped by Maktaba itself.

## When something goes wrong {#troubleshooting}

- **The page says only administrators can open it, but you are one:** your pass expired (it lasts 7 days without the Maktaba service). Use the Maktaba menu on the Mac.
- **Too many tries:** after five failed sign-ins in a minute the page waits five minutes.
- **The database keeps stopping:** safe mode does not help then. Try **Restore a backup**, or **Go back to version …** if an update just installed.
