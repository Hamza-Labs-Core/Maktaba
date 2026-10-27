# Watching Live TV

Watch Maktaba's channels like ordinary TV: browse the guide, tune a channel, flip between channels, and keep watching when you are away from home.

## What it is {#what-it-is}

Maktaba runs live channels made from your library. Each channel plays its schedule around the clock, so when you tune in you join whatever is airing now, just like a TV channel.

There are three places to watch from:

- **TV Guide** at [/guide](/guide): every channel with what is on now and over the next hour and a half.
- **What's On Now** on the Library page: a row of cards with each channel's current programme and a **Tune In** button. It only appears when there are channels you can watch.
- The live player at /live/ followed by the channel number (for example /live/101). This is the address to bookmark or share for a channel.

You only see the channels you are allowed to watch. Channels are made by an admin (see [Channels](/help/channels)).

## How to use it {#how-to-use}

### Use the TV Guide {#guide}

1. Open **TV Guide** from the menu.
2. Each row is a channel; the time runs from left to right. A line marks the current time and the programme airing now shows its progress.
3. Click the programme airing now to tune the channel. Click a later programme to see its details: **Starts at**, **Series**, **Duration** and a description when there is one.
4. Use **Category** to show only one kind of channel (**All categories** shows them all).
5. Click **What's on now** for a simple list of cards, one per channel, with the current programme, its progress and what is **Next**. Click **Grid view** to go back. Phones open the card list by default.

The guide refreshes by itself every 30 seconds. Short clips between programmes are merged into one **Up next** block.

### Watch a channel {#player}

When a channel starts, a banner shows its number, name, the programme on now with its progress, and what is next. It hides after a few seconds. The badge in the corner says **LIVE**.

The buttons under the picture:

- **Open guide** goes back to the TV Guide.
- **Channel up** and **Channel down** move to the next or previous channel number.
- **Last channel** jumps back to the channel you watched before.
- **Mini-guide** opens a list of what is on now on every channel; click one to tune it.
- **Watch from beginning** opens the programme on air in the normal video player, from its start. It shows only when the programme is a video from your library.
- **Picture in picture** keeps the channel playing in a small window.
- **Tap for sound** appears when the browser started the channel muted. Click it to turn the sound on.

While you switch channels, the old channel keeps playing until the new one is ready, and a card shows the new channel's number, name and programme.

### Keyboard and remote control keys {#keys}

- Up arrow, Page Up or a remote's CH+ : next channel. Down arrow, Page Down or CH− : previous channel.
- Type a channel number: it tunes after Enter or after a short pause. **No channel** followed by the number means it does not exist.
- L or Backspace: last channel.
- I: open or close the mini-guide. G: open the TV Guide.
- Enter: show or hide the banner. Escape: close the mini-guide.

On a phone or tablet, swipe up or down on the picture to change channel.

### Pause and return to live {#return-to-live}

You can pause or drag back a little with the player's own controls. The badge then says **Behind live** and a **Return to live** button appears. Click it to jump back to what is airing now.

### Watch away from home {#remote}

When you open Maktaba away from home, the player sends the channel through Cloudflare instead of your home network. The badge then shows **Remote** next to **LIVE**. You do not need to do anything: tuning works the same way.

- Starting a remote channel can take a little longer. The card says **Tuning remotely…** and then **Starting remote stream…** while it starts.
- If a channel keeps failing at home and remote viewing is available, a **Watch remotely** button appears. Click it to try the remote path.
- Your admin decides who may watch away from home, how many channels you may watch at once and the monthly budget. If something is refused, see [When something goes wrong](#troubleshooting). Admins: see [Watching away from home](/help/remote-tv).

## What the states and messages mean {#states}

- **LIVE**: you are watching what airs now.
- **Behind live**: you paused or went back. Click **Return to live** to catch up.
- **Remote**: you are watching away from home, through Cloudflare.
- **Tuning…** / **Tuning remotely…**: the channel is starting.
- **Starting remote stream…**: Maktaba is starting the remote stream for this channel. It can take up to about a minute and a half.
- **Reconnecting…**: the stream stalled and the player is joining it again by itself.
- **No content**: the channel has nothing scheduled at that time.
- **Up next**: a block of short clips between programmes.

## FAQ {#faq}

### Why does a channel start in the middle of a programme? {#faq-middle}

Channels are live. Like TV, you join what is airing now. Use **Watch from beginning** to see the current programme from its start in the normal player.

### Why is there no sound? {#faq-no-sound}

Many browsers start video muted until you click the page. Click **Tap for sound**.

### Can I keep a link to a channel? {#faq-link}

Yes. The address /live/ plus the channel number always opens that channel, for example /live/101.

### Why can't I see a channel the others can? {#faq-missing-channel}

You only see channels you have access to, and only channels that are switched on. Ask your admin.

### Does watching away from home cost anything? {#faq-remote-cost}

It uses your home's Cloudflare account, which bills by minutes watched. Your admin sets a monthly budget; when it is reached, new remote viewing may pause until the next month.

## When something goes wrong {#troubleshooting}

### Could not load the guide. {#guide-load-error}

- **What it means:** the TV Guide could not get the channel list from the server.
- **Does Maktaba fix it itself?** It tries again every 30 seconds while the page is open.
- **What you can do:** check your connection and reload the page. If it keeps happening, tell your admin.

### No channels yet {#no-channels}

- **What it means:** there is no channel you can watch, either because none exist or none are switched on for you.
- **Does Maktaba fix it itself?** No.
- **What you can do:** an admin can click **Create a channel**. Otherwise, ask your admin.

### Channel not found {#channel-not-found}

- **What it means:** that channel number does not exist, is switched off, or you cannot access it.
- **Does Maktaba fix it itself?** No.
- **What you can do:** click **Open guide** and pick a channel from the list.

### Channel unavailable — no content right now. {#channel-unavailable}

- **What it means:** the channel could not start, often because nothing is scheduled or the machine that plays it is busy starting up.
- **Does Maktaba fix it itself?** The player retries a few times before it shows this.
- **What you can do:** try again in a minute or pick another channel. The message may also offer a **stream link** you can open in a player such as VLC. If it keeps happening, tell your admin.

### This browser can't play live streams. {#no-hls}

- **What it means:** your browser cannot play this kind of live video.
- **Does Maktaba fix it itself?** No.
- **What you can do:** use a current version of Chrome, Edge, Firefox or Safari, or open the **stream link** in a player such as VLC.

### Reconnecting… {#reconnecting}

- **What it means:** the picture stopped moving for a few seconds, usually because of the network.
- **Does Maktaba fix it itself?** Yes. It rejoins the live stream by itself, several times if needed.
- **What you can do:** wait a moment. If it keeps coming back, check your connection or change channel and back.

### Remote viewing paused: the monthly budget is reached {#remote-budget}

- **What it means:** this month's Cloudflare budget for watching away from home is used up.
- **Does Maktaba fix it itself?** It starts again on its own at the start of the next month (UTC). Channels already playing remotely keep going.
- **What you can do:** watch at home, or ask your admin to raise the budget.

### Too many remote streams {#remote-user-full}

- **What it means:** you are already watching the most channels you are allowed to watch away from home at once. Each device counts.
- **Does Maktaba fix it itself?** No. A stream frees up when you stop watching it.
- **What you can do:** close the channel on your other device or tab, then try again.

### All remote channels are in use {#remote-channels-full}

- **What it means:** the most channels Maktaba may send to Cloudflare at once are already being sent.
- **Does Maktaba fix it itself?** A slot frees up a few minutes after the last viewer of a channel stops.
- **What you can do:** pick a channel someone else is already watching remotely, or try again later.

### All remote channels are in use (the Cloudflare storage is full) {#remote-storage-full}

- **What it means:** the Cloudflare account has no room for another channel right now.
- **Does Maktaba fix it itself?** Room frees up when a running channel ends.
- **What you can do:** try again later, or ask your admin (they can add storage or shorten the longest broadcast).

### Remote viewing is not set up {#remote-not-set-up}

- **What it means:** watching away from home is not set up, is switched off, or cannot serve viewers right now. A reason may follow in brackets.
- **Does Maktaba fix it itself?** No.
- **What you can do:** watch at home, or tell your admin.

### You may not watch away from home {#remote-not-allowed}

- **What it means:** your admin has not allowed your account to watch away from home.
- **Does Maktaba fix it itself?** No.
- **What you can do:** watch at home, or ask your admin to allow you.

### The remote stream did not start. Try again in a moment. {#remote-timeout}

- **What it means:** the channel did not start on Cloudflare in time.
- **Does Maktaba fix it itself?** No, but the next try often works.
- **What you can do:** wait a moment and tune the channel again.

### Can't reach the remote stream host (\*.cloudflarestream.com). It may be blocked on this network. {#remote-blocked}

- **What it means:** your device could not reach Cloudflare's video servers. Some work, school or hotel networks block them. Your admin may also allow viewing only from some countries.
- **Does Maktaba fix it itself?** No.
- **What you can do:** try another network, such as mobile data. If you are abroad, ask your admin whether your country is allowed.
