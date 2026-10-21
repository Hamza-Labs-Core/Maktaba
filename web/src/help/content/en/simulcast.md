# Simulcast

Send a Maktaba channel on to YouTube, Facebook or any RTMP destination, all the time or only while chosen programmes air.

## What it is {#what-it-is}

Simulcast sends a channel to a live-streaming platform at the same time as it airs in Maktaba. Your home uploads the channel once to your Cloudflare Stream account, and Cloudflare passes it on to every destination you attach. Adding a second destination does not use more of your home's upload.

Each output is billed by Cloudflare as minutes watched, whether anyone watches or not: one output that runs all the time is about 720 output-hours (about $43) a month. Maktaba counts the month's output-hours against a cap you set.

Simulcast is for admins. It lives on [Live TV access](/admin/live-tv), in the **Simulcast** card.

## How to use it {#how-to-use}

### Before you start {#before-you-start}

1. Set up Cloudflare Stream and pass **Test connection** (see [Watching away from home](/help/remote-tv#set-up)). Remote viewing itself may stay off.
2. Open [Settings → Rules](/settings/rules), section **Simulcast**, and switch on **Simulcast to YouTube, Facebook and RTMP** (see [the rules reference](/help/rules#tv-simulcast)).

Until then the card says **Simulcast is switched off (Settings → Rules → Simulcast).**

### Add a destination {#add-destination}

1. Under **Destinations**, click **Add destination**.
2. Give it a **Name** and choose the **Kind**: **YouTube Live**, **Facebook Live** or **Custom RTMP/RTMPS**.
3. The **Ingest URL** is filled in for YouTube and Facebook; change it only if the platform shows another. For a custom destination, enter an rtmp:// or rtmps:// address.
4. Paste the **Stream key** from YouTube Studio or Facebook Live Producer. It is stored encrypted on your home server and never shown again; the list shows only its last characters.
5. Click **Save**.

**Test** checks that the address is valid and a key is saved. Whether the platform accepts the key shows on the platform once an output runs.

To change a destination, click **Edit**. Leave **Stream key** empty to keep the current key. **Delete** removes a destination and its channel attachments; it is greyed out while a channel is sending to it (**in use**).

### Attach a channel {#attach}

1. Under **Channels**, pick a channel in **Channel** and click **Simulcast…**.
2. Tick each destination this channel should go to. You can attach at most the number of destinations shown at the top (**Destinations per channel** in the rules).
3. For each one, tick **On** and choose **When**: **Always** sends it all the time; **On a schedule** sends it only while one of its rules matches (see [Schedule rules](#rules)).
4. Click **Save**. Whenever you turn an attachment on, Maktaba first asks you to confirm your rights (see [Confirm your rights](#rights)).

### Schedule rules {#rules}

A schedule needs at least one rule. Click **Add rule** for more, **Remove rule** to drop one. The kinds:

- **Weekly time window**: tick the **Days** and set **From** and **To**. Times are in the channel's time zone. An end before the start runs past midnight. No day ticked means every day.
- **While a programme airs**: enter the **Video id** of that video (the id in the video's address in Maktaba).
- **While a series airs**: enter the **Series id** (the id in the series' address).
- **During live slots**: whenever the channel airs one of its live broadcasts.

The output starts a little before each window (**Start before a window (seconds)**, so the platform has the stream when the programme starts) and stops a little after (**Stop after a window (seconds)**).

### Confirm your rights {#rights}

YouTube and Facebook check live video against copyright claims. Each time you turn an attachment on, a **Confirm your rights** box asks you to confirm that you hold the rights to everything the channel airs while it is simulcast, including programmes, clips and promos. Click **I hold the rights: turn it on**. Your confirmation is recorded with your name and the time, and shows in the **Rights confirmed** column.

### Start or stop by hand {#manual}

Each row under **Outputs** has **Start now** or **Stop now**:

- On a scheduled output, **Start now** sends it until the next window boundary; **Stop now** keeps it off until the next window boundary. The row then says **started by hand** or **stopped by hand**, with **until** and the time.
- On an **Always** output, **Stop now** keeps it off until you click **Start now**.

**Start now** only works on an attachment that is **On**.

### Watch the monthly cap {#cap}

The card shows **This month: … of … output-hours (about $…).** Set the cap in the rules with **Output-hours a month** (across all destinations, per calendar month, UTC). At 80 % you are told. At 100 % no output starts and **Always** outputs stop; scheduled outputs that are live finish their window.

## What the states and messages mean {#states}

### Output status {#output-states}

- **Scheduled**: waiting for its next window. **Next:** shows when it starts.
- **Starting**: being set up at Cloudflare.
- **Live**: being sent. **until** shows when a scheduled window ends. The platform's own view of the stream shows on the platform.
- **Stopping**: being taken down.
- **Off**: not sent now. A reason may show under it.
- **Error**: Cloudflare could not send it; the detail shows under it. **Cloudflare:** shows Cloudflare's own status when there is one.
- **off** next to the channel: the attachment is switched off.

### Why an output is not sending {#reasons}

- **Waiting: every remote channel is in use.**: all of Cloudflare's channel slots are taken. It starts when one frees up.
- **This month's output-hours are used up.**: the monthly cap is reached.
- **This month's remote budget is used up.**: the remote viewing budget is reached; no new output starts, running ones finish their window.
- **Cloudflare Stream is not ready: run Test connection.**
- **Simulcast is switched off.**: switch it on in the rules.
- **The broadcast is reconnecting; the output stays attached.**: Maktaba's upload to Cloudflare is reconnecting. Nothing to do.
- **The window closed.**: the scheduled time is over.
- **Stopped by the admin.**: someone clicked **Stop now**.
- **The channel is disabled.**: switch the channel on in [Channels](/admin/channels).

## FAQ {#faq}

### Does simulcast need remote viewing switched on? {#faq-remote}

No. It needs Cloudflare Stream set up with a passed test, but works with remote viewing off.

### Does each destination use more of my upload? {#faq-upload}

No. Your home sends the channel once; Cloudflare copies it to each destination. Each destination does add to the output-hours and the bill.

### Where do I see whether YouTube received it? {#faq-platform}

In YouTube Studio or Facebook Live Producer. Maktaba shows that it is sending; the platform shows what it receives.

### Can I send one channel to several platforms? {#faq-several}

Yes, up to **Destinations per channel** (Cloudflare allows up to 50).

## When something goes wrong {#troubleshooting}

### Test says "Not ready" {#test-not-ready}

- **What it means:** the ingest URL is not a valid rtmp:// or rtmps:// address, or no stream key is saved.
- **Does Maktaba fix it itself?** No.
- **What you can do:** click **Edit**, fix the URL or paste the key, and save.

### Simulcast cannot start outputs {#not-ready}

- **What it means:** simulcast is on but cannot start outputs; the reason follows (often Cloudflare Stream is not ready).
- **Does Maktaba fix it itself?** No.
- **What you can do:** run **Test connection** on the Cloudflare Stream card in [Extensions](/admin/extensions#cloudflare_stream).

### Output shows Error {#output-error}

- **What it means:** Cloudflare could not create or keep the output.
- **Does Maktaba fix it itself?** Yes. It tries again after about a minute while the output is still wanted.
- **What you can do:** read the detail under the status. Check the ingest URL and stream key, and that the platform's live stream is ready to receive.

### The monthly cap is reached: no output starts. {#cap-reached}

- **What it means:** the month's output-hours reached **Output-hours a month**.
- **Does Maktaba fix it itself?** Outputs can start again at the start of the next month (UTC).
- **What you can do:** raise the cap in the rules, or move outputs from **Always** to a schedule.

### Confirm that you hold the rights before turning it on. {#rights-required}

- **What it means:** an attachment was turned on without the rights confirmation.
- **Does Maktaba fix it itself?** No.
- **What you can do:** save again and click **I hold the rights: turn it on** in the box that appears.

### Turn the attachment on first. {#attachment-off}

- **What it means:** you clicked **Start now** on an attachment that is switched off.
- **Does Maktaba fix it itself?** No.
- **What you can do:** open **Edit**, tick **On**, save, then start it.

### Too many destinations for one channel. {#too-many}

- **What it means:** more destinations are ticked than **Destinations per channel** allows.
- **Does Maktaba fix it itself?** No.
- **What you can do:** untick one, or raise **Destinations per channel** in the rules.

### A channel is simulcasting to this destination: stop or detach it first. {#destination-in-use}

- **What it means:** you tried to delete a destination a channel is sending to.
- **Does Maktaba fix it itself?** No.
- **What you can do:** click **Stop now** on that output, or untick the destination in the channel, then delete it.

### The destination, attachment or schedule is not valid {#invalid}

- **What it means:** a field is missing or wrong; the message names it.
- **Does Maktaba fix it itself?** No.
- **What you can do:** fix the field named and save again.

### Simulcast is not available on this server. {#unavailable}

- **What it means:** this server does not offer simulcast.
- **Does Maktaba fix it itself?** No.
- **What you can do:** open it on your home server, where Cloudflare Stream is set up.
