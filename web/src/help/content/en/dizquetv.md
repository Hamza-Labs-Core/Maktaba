# dizqueTV

Send Maktaba's planned channels to a dizqueTV server, and check that what each channel airs fits the channel.

## What it is {#what-it-is}

dizqueTV is a separate program that makes live TV channels out of a Plex library. When the dizqueTV extension is on, Maktaba writes to it in two steps and then checks the result:

- **dizqueTV show**: every season Maktaba detects becomes a custom show in dizqueTV, with its episodes in order.
- **dizqueTV channels**: the latest channel plan is written into dizqueTV: each channel's programmes, filler lists, schedule, logo, corner watermark and guide settings. Every other setting of an existing channel stays as it is.
- **Channel audit**: after the channels are written, Maktaba reads them back from dizqueTV and checks that what each channel airs is about the channel. It writes nothing to dizqueTV.

dizqueTV plays your videos from Plex, so dizqueTV needs the [Plex](/help/plex) extension set up too, and your Plex server registered inside dizqueTV.

Maktaba is careful with your dizqueTV server:

- It only changes or removes channels, shows and filler lists it made itself. Anything else with the same name or number is left alone and reported, unless you choose **Take over what exists**.
- Every object it writes is read back to check it.
- By default a rewrite does not interrupt what is on air (see **Rewrite without a break** below).

## How to use it {#how-to-use}

### Connect dizqueTV {#connect}

1. Set up Plex first (see [Plex](/help/plex#connect)), and in dizqueTV add the same Plex server.
2. Open [Extensions](/admin/extensions#dizquetv), enter **dizqueTV URL** (for example http://your-server:8000) and click **Save settings**.
3. Click **Test connection**. **Plex servers in dizqueTV** should name the Plex server Maktaba uses.
4. Switch on **Enabled**.

From then on, the shows are written when seasons change, and the channels are written each time a new channel plan is made. You can follow the steps in [Processing](/queue); see [Stages](/help/stages#dizquetv-show).

### Choose how channels are written {#export-rules}

In [Settings → Rules](/settings/rules), section **dizqueTV export** (see [the rules reference](/help/rules#dizquetv)):

- **Remove what the plan no longer has**: delete the channels, shows and filler lists Maktaba made earlier that the plan does not have now. Off by default.
- **Also remove other channels not in the plan**: with the setting above, every channel not in the plan goes, including ones you made by hand. For a fresh lineup only.
- **Take over what exists**: a channel, show or filler list with the same number or name that Maktaba did not make is replaced. Off: it is left alone and reported.
- **Use the Ramadan grids**: channels with a Ramadan grid use it.
- **Rewrite without a break**: the programme on air finishes, each show goes on where it stopped, and a channel whose schedule did not change is not saved at all, so nobody watching is restarted. On by default.
- **Only these channels**: channel numbers separated by commas. Empty means all of them.
- **Live broadcasts**: live streams a channel carries at set times, such as the news on the hour.

### Review changes before they are written {#review}

On the dizqueTV step's page in [Processing](/queue), set **Writes to other systems** to **Review first**. Each change then waits with **Waiting for your review** until you approve it. See [Processing](/help/processing#waiting-awaiting-review).

### Read the channel audit {#audit}

The audit runs by itself after the channels are written. Open [Reports](/queue/reports) and pick **Channel audit**. For each channel it shows what share of the time it airs fits its theme, the filler clips and which shows or clips do not fit.

In [Settings → Rules](/settings/rules), section **Channel audit** (see [the rules reference](/help/rules#audit)):

- **Audit the channels**: switch the audit on or off.
- **Days weighed**: how many days ahead the audit looks at (28 by default).
- **Audits kept**: how many past audits are kept.

## What the states and messages mean {#states}

- A season whose episodes are not all known to Plex yet waits, and is written when Plex has caught up.
- The channels step waits when there is no channel plan yet. It runs when the plan is made.
- Items that are left alone because Maktaba did not make them show as conflicts in the step's log.
- **Waiting for your review**: the step is set to **Review first** and a change is waiting for you.
- The audit shows as unchanged when **Audit the channels** is off.

## FAQ {#faq}

### Will Maktaba delete my own dizqueTV channels? {#faq-delete}

Not unless you switch on both **Remove what the plan no longer has** and **Also remove other channels not in the plan**. By default it only touches what it made.

### Does rewriting a channel interrupt viewers? {#faq-interrupt}

Not with **Rewrite without a break** on: the programme on air finishes and unchanged channels are not saved at all.

### Do I need dizqueTV to have live channels? {#faq-needed}

No. Maktaba has its own live channels (see [Watching Live TV](/help/watch-tv)). dizqueTV is only for people who already use it.

### Why does dizqueTV need Plex? {#faq-plex}

dizqueTV plays from Plex. Every programme Maktaba writes points to a Plex item, so the Plex extension must be set up and Plex must know your videos.

## When something goes wrong {#troubleshooting}

### dizqueTV is unreachable {#dizquetv-unreachable}

- **What it means:** Maktaba could not reach the dizqueTV server: it is off, restarting, or the address changed.
- **Does Maktaba fix it itself?** Yes. The step shows that dizqueTV is not answering and retries after a pause.
- **What you can do:** check that dizqueTV is running and that **dizqueTV URL** is right, then run **Test connection**. See [Failures](/help/failures#dizquetv-unreachable).

### Write did not read back correctly {#readback-mismatch}

- **What it means:** after writing a show, filler list or channel, dizqueTV did not hold what Maktaba wrote, or something Maktaba removed was still there.
- **Does Maktaba fix it itself?** No.
- **What you can do:** check that nobody else is editing dizqueTV at the same time, then **Retry**. See [Failures](/help/failures#readback-mismatch).

### dizqueTV has no Plex server registered {#no-plex-server}

- **What it means:** dizqueTV does not have the Plex server Maktaba uses, so it could not play the programmes.
- **Does Maktaba fix it itself?** No.
- **What you can do:** in dizqueTV, add the same Plex server Maktaba uses (same address), then **Retry**. **Test connection** shows it under **Plex servers in dizqueTV**.

### Writing dizqueTV programmes needs the Plex settings {#plex-not-configured}

- **What it means:** the Plex extension has no address or token, and dizqueTV programmes are built from Plex.
- **Does Maktaba fix it itself?** No.
- **What you can do:** set up Plex in [Extensions](/admin/extensions#plex), then **Retry**.

### A channel, show or filler list was left alone {#conflict}

- **What it means:** dizqueTV already has one with the same name or number that Maktaba did not make, so Maktaba did not overwrite it.
- **Does Maktaba fix it itself?** No. It never overwrites what you made unless you allow it.
- **What you can do:** rename or renumber yours in dizqueTV, or switch on **Take over what exists**.
