# Plex

What Maktaba does in your Plex server: it keeps a collection for every season it finds, adds topic and clip collections, and puts Maktaba's channels in Plex Live TV with their guide.

## What it is {#what-it-is}

When the Plex extension is on, Maktaba writes to Plex in three ways:

- **Season collections**: every season Maktaba detects becomes a manual Plex collection with its episodes in order.
- **Topic and clip collections**: a library's topics become collections named with "ملف", and its clip groups become collections named with "مقاطع" (clips) or "منفردات" (stand-alone videos).
- **Plex Live TV**: Maktaba's tuner and guide are added to Plex, and Plex's channel list and guide are kept up to date when your channels change.

To do this, Maktaba first reads each library's list of items from Plex once (the **Plex index**) and matches them to your files. Later steps use that list instead of asking Plex item by item.

Maktaba is careful with your Plex server:

- Every collection it creates carries the label **Maktaba-auto**. It only ever changes or deletes collections it created.
- If a collection with the same title already exists and Maktaba did not make it, Maktaba leaves it alone and reports it.
- After each write it reads the collection back from Plex to check it.
- In Live TV, other tuners (an antenna, dizqueTV) are never changed.

## How to use it {#how-to-use}

### Connect Plex {#connect}

1. Open [Extensions](/admin/extensions#plex) and click **Sign in with Plex** on the Plex card (see [Extensions](/help/extensions#plex)).
2. Pick your server and click **Use this server**.
3. If Plex sees your files under different paths than Maktaba does (for example Plex runs on the NAS), fill in **Path map**. The **Path map** check of **Test connection** tells you whether your library folders are found in a Plex library.
4. Click **Test connection**, then switch on **Enabled**.

From then on the Plex steps run by themselves whenever a library is scanned or its seasons change. You can follow them in [Processing](/queue); see [Stages](/help/stages#plex-index) for each step.

### Review Plex changes before they are made {#review}

On a Plex step's page in [Processing](/queue), **Writes to other systems** can be set to **Review first**. Each change then waits with **Waiting for your review** until you approve it. See [Processing](/help/processing#waiting-awaiting-review).

### Add Maktaba's channels to Plex Live TV {#live-tv}

1. On [Live TV access](/admin/live-tv), turn the tuner on (see [Live TV administration](/help/live-tv-admin)).
2. In the **Plex** card, click **Add Maktaba to Plex**. A box lists what will change in your Plex server; click **Add to Plex**.
3. Maktaba's tuner joins the DVR shown on Plex's **Live TV & DVR** page as another channel source, with Maktaba's guide. With no DVR in Plex yet, a new one is made. Maktaba's channels are switched on and the guide is reloaded.
4. Plex takes a few minutes to load the guide. The card says so; refreshing meanwhile does not speed it up.

**Refresh Plex now** sends the channel list and reloads the guide at once.

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

### Collections {#collection-states}

- A season whose episodes are not all known to Plex yet waits and runs again when Plex has caught up.
- A same-title collection Maktaba did not create is left alone; the step waits and its log says so.
- A topic or clip collection whose topic is gone is deleted only when the library's current collections hold most of its items; otherwise it is kept and reported.

## FAQ {#faq}

### Will Maktaba delete my own Plex collections? {#faq-delete}

No. It only changes or deletes collections labelled **Maktaba-auto** that it created itself.

### Why are some videos missing from a collection? {#faq-missing}

Plex has not added them yet, or Plex sees them under a different path. Let Plex scan its library, and check the **Path map** check in [Extensions](/admin/extensions#plex).

### Do I need Plex Pass for Live TV? {#faq-plex-pass}

Yes. Plex Live TV needs Plex Pass on the server owner's account. Collections work without it.

### Does adding Maktaba change my antenna or dizqueTV channels? {#faq-other-tuners}

No. Other tuners keep their channels, names and guides. Maktaba's guide reload does reload every guide in the shared DVR.

## When something goes wrong {#troubleshooting}

### Plex rejected the token {#plex-auth}

- **What it means:** Plex refused Maktaba's token, usually because you signed out, changed your password or removed the device in Plex.
- **Does Maktaba fix it itself?** No. The Plex steps keep failing until the token works again.
- **What you can do:** click **Sign in with Plex** on the [Plex card](/admin/extensions#plex), then **Retry** the failed items. See [Failures](/help/failures#plex-auth).

### Plex is unreachable {#plex-unreachable}

- **What it means:** Maktaba could not reach the Plex server: it is off, restarting, or the address changed.
- **Does Maktaba fix it itself?** Yes. The step shows that Plex is not answering and retries after a pause.
- **What you can do:** check that Plex is running and that **Plex URL** is right, then run **Test connection**. See [Failures](/help/failures#plex-unreachable).

### Write did not read back correctly {#readback-mismatch}

- **What it means:** after writing a collection, Plex did not hold the expected items or order.
- **Does Maktaba fix it itself?** No, but a retry usually works once Plex has finished its own scan.
- **What you can do:** let Plex finish scanning, then **Retry**. See [Failures](/help/failures#readback-mismatch).

### A collection with the same title exists {#title-conflict}

- **What it means:** Plex already has a collection with that title that Maktaba did not create, so Maktaba leaves it alone.
- **Does Maktaba fix it itself?** No. It never touches collections it did not make.
- **What you can do:** rename or delete your own collection in Plex if you want Maktaba's, then use **Re-queue a library or season…** on the step's page in [Processing](/queue).

### Path map does not match {#path-map}

- **What it means:** your library folders are not folders of any Plex library as Maktaba sees them, so videos cannot be matched to Plex items.
- **Does Maktaba fix it itself?** No.
- **What you can do:** set **Path map** on the Plex card to how Plex sees your share (for example smb://nas/Share/=/volume1/Share/), save, and run **Test connection**.

### Live TV is not available on this server {#livetv-missing}

- **What it means:** the Plex server has no Live TV, usually because the owner's account has no Plex Pass, or Live TV has no DVR yet.
- **Does Maktaba fix it itself?** No.
- **What you can do:** get Plex Pass for the server owner, or add Maktaba with **Add Maktaba to Plex**, which makes a DVR when none exists.

### Channels or guide do not show in Plex {#guide-missing}

- **What it means:** Plex has not loaded the guide yet, or the address Plex uses for the tuner is wrong.
- **Does Maktaba fix it itself?** It pushes the channel list and reloads the guide when channels change, if the automatic options are on.
- **What you can do:** wait a few minutes after adding, then click **Refresh Plex now**. If it still fails, check the tuner and guide addresses under **Plex Live TV** in the rules: they must be reachable from the Plex server.

### Plex's channel list was changed by another program {#channel-map-drift}

- **What it means:** Plex showed other channels on Maktaba's tuner than Maktaba's own, usually dizqueTV's numbers. dizqueTV's Plex refresh writes its channel list onto every tuner in the DVR, Maktaba's too, so Plex shows no Maktaba channels until Maktaba puts them back.
- **Does Maktaba fix it itself?** Yes, when **Refresh the channels in Plex** is on: it checks Plex every **Check Plex's channel list every (min)** (15 by default) and puts its channels back, then reloads the guide. The card then says **restored**. With that option off it only reports it (**not restored**).
- **What you can do:** when it keeps happening, the card says **Another program replaced Maktaba's channels in Plex … times in a row**. In dizqueTV, open Settings → Plex and turn off its channel mapping ("auto refresh channels"); dizqueTV's own guide refresh can stay on. The count and the interval are under **Plex Live TV** in the [rules](/help/rules#plexlive).
