# Watching away from home

Let people watch Maktaba's channels when they are not at home, through your own Cloudflare Stream account: who may watch, how many at once, what it costs and how to keep it within budget.

## What it is {#what-it-is}

At home, the TV player gets each channel straight from your Maktaba server. Away from home, Maktaba sends the channel once to your Cloudflare Stream account, and Cloudflare serves it to every remote viewer. Your home uploads each channel once, however many people watch it.

Cloudflare bills your account for minutes watched and for the storage a running broadcast uses. Maktaba estimates the month's cost, warns you at 80 % of your budget and can refuse new remote viewing at 100 %.

Viewers do nothing special: when they open Maktaba away from home, the player switches to the remote path by itself (see [Watching Live TV](/help/watch-tv#remote)). Plex viewers away from home are different: they use Plex's own remote access, and Maktaba sends nothing to Cloudflare for them.

Everything here is for admins.

## How to use it {#how-to-use}

### Set up Cloudflare Stream {#set-up}

1. Open [Extensions](/admin/extensions#cloudflare_stream) and find the **Cloudflare Stream** card.
2. Fill in **Account ID** and the **API token** (it needs Stream Write and Account Analytics Read). Leave the customer subdomain code empty: **Test connection** finds it.
3. Click **Save settings**, then **Test connection**. Each check gets a line under **Connection checks** (see [Connection checks](#checks)).
4. When the test passes, switch on **Remote viewing**. The switch only works after a passed test from the last 24 hours.

The card then shows **Remote viewing is on**, the playback domain and **signing key ready**.

### Choose who may watch and the limits {#limits}

Open [Settings → Rules](/settings/rules) and the **Maktaba TV away from home** section (see [the rules reference](/help/rules#tv-remote)):

- **Who may watch away from home**: all signed-in users, admins only, or the users listed in **Users allowed (listed)**.
- **Remote streams per user**: how many channels one person may watch away from home at once. Each device counts.
- **Remote channels at once**: how many channels may be sent to Cloudflare at the same time. Each one uses its bitrate of your home's upload once.
- **Countries**: two-letter country codes viewers may watch from. Empty means anywhere.
- **Keep sending after the last viewer (minutes)**: so a viewer who comes back finds the channel at once.
- **Playback link lifetime (minutes)**: a viewer's link stops working after this; the player renews it while it plays. Shorter cuts off a removed user sooner.
- **Longest broadcast (hours)**: after this, a channel's broadcast starts again (remote viewers re-buffer once), so its recording never outgrows your storage.
- **Who counts as away from home**: requests through the Maktaba relay, or also requests from a public address (your own reverse proxy or tunnel).

### Set the monthly budget {#budget}

In the same rules section:

- **Monthly budget (USD)**: the estimated Cloudflare spend per calendar month (UTC). 0 turns the budget check off.
- **At 100 % of the budget**: block_new refuses new remote viewing while running channels finish; warn only shows a notice.
- **Price per 1,000 minutes watched** and **Price per 1,000 minutes of storage a month**: what the estimate uses. Set them to your Cloudflare prices.
- **Storage plan (minutes)** and **Storage kept free (minutes)**: the storage guard (see [Storage](#storage)).

There is no saving from audio-only viewing: Cloudflare bills minutes watched, not bytes.

### Follow use and cost {#usage}

The **Away from home: use and limits** box shows on the Cloudflare Stream card and under the rules section. It lists:

- the month's minutes watched and the estimated cost, against the budget;
- how many remote channels are in use out of the maximum;
- the stored minutes against your storage, with what running broadcasts reserve;
- the highest stored minutes this month and the upload in use now.

Under **Channels sent to Cloudflare**, each running channel has a **Stop** button that ends its broadcast now.

[Live TV access](/admin/live-tv) has a **Remote viewing** card with more detail, refreshed every 5 seconds: the status line, the month's cost, the **Live inputs**, the **Channels sent to Cloudflare (last 24 h)** with the viewers Maktaba and Cloudflare count, the **Remote sessions** (user, device, channel) and the **Last remote events**.

### Storage {#storage}

While a channel is sent to Cloudflare, Cloudflare records it, and the recording is deleted when the broadcast ends. Before it starts another channel, Maktaba checks that every running broadcast's full length (up to **Longest broadcast**), the recordings not yet deleted and **Storage kept free** fit in your account's storage. If not, the new channel is refused. On one 1,000-minute storage block, this usually allows about two channels at once. To fit more, buy more storage at Cloudflare or lower **Longest broadcast**.

### Rotate the signing key {#rotate-key}

Remote playback links are signed. **Rotate signing key** (in the use and limits box) makes a new key and deletes the old one at Cloudflare. Every remote link stops working at once; players fetch a new one within about a minute. Use it if you think a link was shared, or to cut off a user right away after removing their access.

### Switch off or remove {#remove}

- Switch **Remote viewing** off on the card to stop remote viewing and keep the setup.
- **Remove integration** stops remote viewing, deletes Maktaba's live inputs and signing key in your Cloudflare account, and deletes the stored token here. You confirm first.

## Connection checks {#checks}

**Test connection** runs these checks. Each shows **ok**, **failed**, **missing, optional** or **not checked**, with a reason:

- **API token**: Cloudflare accepts the token.
- **Stream on the account**: Cloudflare Stream is available on the account.
- **Storage**: the account has Stream storage, and how much is used. A live channel is recorded while it runs, so a storage plan is needed.
- **Analytics (optional)**: the token can read minutes watched. Without it, the cost is estimated from Maktaba's own sessions.
- **Signing key**: a signing key for playback links is there or could be made.
- **Playback domain**: your account's customer-…cloudflarestream.com address.
- **Push from this server**: this server's video tools can send a channel to Cloudflare the way **How channels are sent** asks (rtmps or srt).

## What the states and messages mean {#states}

### The status line {#status}

- **Cloudflare Stream is not set up.**: no account is saved yet. **Set it up** opens the Extensions card.
- **Cloudflare Stream is set up; remote viewing is switched off.**: switch on **Remote viewing** on the card.
- **Remote viewing is on but cannot serve viewers now**: the reason follows. Fix it, then run **Test connection**.
- **Remote viewing is on and ready.**: all is well.
- **Last test passed** / **Last test failed**, with the time: the result of the last **Test connection**.
- **Not tested yet.**: save the account ID and token, then run **Test connection**.

### Channels sent to Cloudflare {#push-states}

- **starting**, **connecting**: the channel is being sent.
- **live**: Cloudflare is receiving it and viewers can watch.
- **stopping**, **cleaning up**: the broadcast is ending and its recording is being deleted.
- **ended**: finished normally.
- **failed**: it stopped with an error, shown after the dash.

### Live inputs {#input-states}

An input is a slot in your Cloudflare account that receives one channel.

- **idle**: free. **assigned**: given to a channel. **broadcasting**: receiving a channel.
- **cleaning up**: being emptied. **errored**: Cloudflare reported a problem, shown in the Cloudflare column.

### Budget notices {#budget-notices}

- At 80 % of the budget, a notice says remote viewing reached 80 % of the monthly budget.
- At 100 %, the notice says either **New remote tunes are refused until the month ends or the budget is raised.** or **Remote viewing continues: the budget action is set to warn only.**
- **The budget is used up: new remote tunes are refused; running ones finish.**: see [Budget used up](#budget-used-up).

## FAQ {#faq}

### Does each viewer add to my upload? {#faq-upload}

No. Each channel is uploaded once, whatever the number of remote viewers. Cost grows with minutes watched, not with upload.

### Why is the cost marked "estimated from sessions"? {#faq-estimate}

The token cannot read Cloudflare's analytics, so Maktaba counts minutes from its own remote sessions. Give the token Account Analytics Read for Cloudflare's own figures.

### When does the budget month start? {#faq-month}

On the first of each month, UTC, the same as Cloudflare's billing.

### How do I cut off one user at once? {#faq-cut-off}

Remove them from who may watch, then click **Rotate signing key**. Other viewers get a new link within a minute; the removed user does not.

### Can I use remote viewing only for simulcast? {#faq-simulcast}

Yes. Simulcast needs Cloudflare Stream set up with a passed test, but works with remote viewing switched off. See [Simulcast](/help/simulcast).

## When something goes wrong {#troubleshooting}

### Test connection fails {#test-failed}

- **What it means:** one of the [connection checks](#checks) failed; its line gives the reason.
- **Does Maktaba fix it itself?** No.
- **What you can do:** fix what the line says (often the token's permissions or the account ID), click **Save settings** and run **Test connection** again.

### Remote viewing is unavailable {#unavailable}

- **What it means:** remote viewing is on but cannot serve viewers; the message gives the reason.
- **Does Maktaba fix it itself?** No.
- **What you can do:** fix the reason, then run **Test connection** on the Cloudflare Stream card.

### The budget is used up {#budget-used-up}

- **What it means:** the month's estimated cost reached the budget. With block_new, new remote viewing is refused; viewers see that the monthly budget is reached.
- **Does Maktaba fix it itself?** It starts again at the start of the next month (UTC). Running channels finish.
- **What you can do:** raise **Monthly budget (USD)**, or set **At 100 % of the budget** to warn.

### No room for another broadcast {#storage-full}

- **What it means:** your Cloudflare storage cannot hold another channel's recording; viewers see that all remote channels are in use because the storage is full.
- **Does Maktaba fix it itself?** Room frees up when a running broadcast ends.
- **What you can do:** buy another 1,000-minute storage block at Cloudflare, or lower **Longest broadcast (hours)**. You can also **Stop** a channel nobody needs.

### All remote channels are in use {#channels-full}

- **What it means:** **Remote channels at once** is reached.
- **Does Maktaba fix it itself?** A channel stops a few minutes after its last viewer leaves.
- **What you can do:** raise **Remote channels at once** if your upload allows it.

### A channel shows "failed" {#push-failed}

- **What it means:** sending the channel to Cloudflare stopped with an error, shown next to it.
- **Does Maktaba fix it itself?** It retries once before it marks the channel failed. After that, the channel is kept off the remote path for about 10 minutes, then can be tried again.
- **What you can do:** read the error and the **Last remote events**. Check that this server can still reach Cloudflare and that the channel plays at home.

### Viewers can't reach the stream {#viewers-blocked}

- **What it means:** a viewer's network blocks Cloudflare's video servers, or their country is not in **Countries**.
- **Does Maktaba fix it itself?** No.
- **What you can do:** check **Countries**. The viewer can try another network.
