# Plex

What Maktaba does in your Plex server: it puts Maktaba's channels in Plex Live TV with their guide and keeps them up to date. Maktaba is your library; it does not write collections into Plex or read Plex's libraries.

## What it is {#what-it-is}

When the Plex extension is on and signed in, Maktaba adds its own HDHomeRun tuner and its guide to **Plex Live TV**, and keeps Plex's channel list and guide up to date when your channels change.

Maktaba is careful with your Plex server:

- It only adds, changes or removes its own tuner. Other tuners in Live TV (an antenna, another program's tuner) are never changed.
- Before it first adds itself, it shows you what will change.

## How to use it {#how-to-use}

### Connect Plex {#connect}

1. Open [Extensions](/admin/extensions#plex) and click **Sign in with Plex** on the Plex card (see [Extensions](/help/extensions#plex)).
2. Pick your server and click **Use this server**.
3. Click **Test connection**, then switch on **Enabled**.

**Test connection** checks that the server answers, that the token works, which server it is, whether it has Live TV and whether Maktaba's tuner is in it.

### Add Maktaba's channels to Plex Live TV {#live-tv}

1. On [Live TV access](/admin/live-tv), turn the tuner on (see [Live TV administration](/help/live-tv-admin)).
2. In the **Plex** card, click **Add Maktaba to Plex**. A box lists what will change in your Plex server; click **Add to Plex**.
3. Maktaba's tuner joins the DVR shown on Plex's **Live TV & DVR** page as another channel source, with Maktaba's guide. With no DVR in Plex yet, a new one is made. Maktaba's channels are switched on and the guide is reloaded.
4. Plex takes a few minutes to load the guide. The card says so; refreshing meanwhile does not speed it up.

**Refresh Plex now** sends the channel list and reloads the guide at once. The work runs as the **Plex Live TV** step in [Processing](/queue); see [Stages](/help/stages#plex-livetv).

Under **Automatic** you can choose:

- **Add Maktaba to Plex by itself when Plex has no DVR for it**.
- **Refresh the guide in Plex when the schedule changes**.
- **Refresh the channel list in Plex when channels change**.
- **Guide reload at most every (minutes)**: only for reloads caused by the schedule growing. A channel change or **Refresh Plex now** reloads at once. Click **Save interval**.

The tuner and guide addresses Plex is given, the guide language and its name are in [Settings → Rules](/settings/rules), section **Plex Live TV** (see [the rules reference](/help/rules#plexlive)).

Plex viewers away from home use Plex's own remote access: Plex pulls the channel from Maktaba's tuner. Maktaba sends nothing to Cloudflare for them.

### Remove Maktaba from Plex Live TV {#remove}

In Plex, open Settings → Live TV & DVR and remove Maktaba's device there.

## What the states and messages mean {#states}

### The Plex card on Live TV access {#live-tv-states}

- **Plex server**: the address, then **reachable**, **not reachable** or **not checked yet**.
- **Last check**: when Maktaba last looked at Plex.
- **Maktaba in Plex**: **Added: Maktaba is a channel source in Plex's Live TV & DVR, with its own guide**, or **Not added yet**.
- **DVRs in Plex**: each DVR with its tuners: **Maktaba**, or **another tuner (left as it is)**.
- **Channels in Plex**: how many of your channels Plex has.
- **Last guide reload** and **Last channel list push**: when Maktaba last updated Plex.
- **Now**: **Talking to Plex…** while it works, or **Waiting:** with a reason.
- **Last error**: the last problem talking to Plex.
- **Turn on the Plex extension and sign in with Plex first.**: the extension is off or not set up. **Open Extensions** takes you there.
- **Turn the tuner on above before adding Maktaba to Plex.**: switch the tuner on first.
- **Maktaba is in a DVR of its own…**: an older version made a separate DVR that Plex's page does not show. Adding Maktaba again moves it into the DVR Plex shows.

## FAQ {#faq}

### Does Maktaba make collections in Plex? {#faq-collections}

No. Maktaba is your library: seasons, topics and collections live in Maktaba. Collections an older version of Maktaba made in Plex (labelled **Maktaba-auto**) are no longer changed; you can delete them in Plex.

### Do I need Plex Pass for Live TV? {#faq-plex-pass}

Yes. Plex Live TV needs Plex Pass on the server owner's account.

### Does adding Maktaba change my other tuners' channels? {#faq-other-tuners}

No. Other tuners keep their channels, names and guides. Maktaba's guide reload does reload every guide in the shared DVR.

## When something goes wrong {#troubleshooting}

### Plex rejected the token {#plex-auth}

- **What it means:** Plex refused Maktaba's token, usually because you signed out, changed your password or removed the device in Plex.
- **Does Maktaba fix it itself?** No. The Plex Live TV step keeps failing until the token works again.
- **What you can do:** click **Sign in with Plex** on the [Plex card](/admin/extensions#plex), then **Retry** the failed items. See [Failures](/help/failures#plex-auth).

### Plex is unreachable {#plex-unreachable}

- **What it means:** Maktaba could not reach the Plex server: it is off, restarting, or the address changed.
- **Does Maktaba fix it itself?** Yes. The step shows that Plex is not answering and retries after a pause.
- **What you can do:** check that Plex is running and that **Plex URL** is right, then run **Test connection**. See [Failures](/help/failures#plex-unreachable).

### Live TV is not available on this server {#livetv-missing}

- **What it means:** the Plex server has no Live TV, usually because the owner's account has no Plex Pass, or Live TV has no DVR yet.
- **Does Maktaba fix it itself?** No.
- **What you can do:** get Plex Pass for the server owner, or add Maktaba with **Add Maktaba to Plex**, which makes a DVR when none exists.

### Channels or guide do not show in Plex {#guide-missing}

- **What it means:** Plex has not loaded the guide yet, or the address Plex uses for the tuner is wrong.
- **Does Maktaba fix it itself?** It pushes the channel list and reloads the guide when channels change, if the automatic options are on.
- **What you can do:** wait a few minutes after adding, then click **Refresh Plex now**. If it still fails, check the tuner and guide addresses under **Plex Live TV** in the rules: they must be reachable from the Plex server.

### Plex's channel list was changed by another program {#channel-map-drift}

- **What it means:** Plex showed other channels on Maktaba's tuner than Maktaba's own. Another program with a tuner in the same DVR can write its channel list onto every tuner in the DVR when it refreshes Plex, Maktaba's too, so Plex shows no Maktaba channels until Maktaba puts them back.
- **Does Maktaba fix it itself?** Yes, when **Refresh the channels in Plex** is on: it checks Plex every **Check Plex's channel list every (min)** (15 by default) and puts its channels back, then reloads the guide. The card then says **restored**. With that option off it only reports it (**not restored**).
- **What you can do:** when it keeps happening, the card says **Another program replaced Maktaba's channels in Plex … times in a row**. In the other program's Plex settings, turn off its channel mapping (often called "auto refresh channels"); its own guide refresh can stay on. The count and the interval are under **Plex Live TV** in the [rules](/help/rules#plexlive).
