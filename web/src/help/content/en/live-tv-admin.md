# Live TV administration

Let Plex and other players watch Maktaba's channels, choose which machines encode them, and see what is on the air.

## What it is {#what-it-is}

**Live TV access** (Admin, at [/admin/live-tv](/admin/live-tv)) is where you run Maktaba TV. Maktaba behaves like an HDHomeRun tuner with its own XMLTV guide, so Plex, Jellyfin, Emby, VLC and other IPTV players can watch its channels. On the same page you find:

- the **HDHomeRun tuner** switch and its settings;
- **TV changes**: whether changes are staged, and the change in flight (see [TV changes](/help/tv-changes));
- **Streaming hosts** and **On air**: which machines encode channels, and what each channel is doing now;
- **Remote viewing** (see [Watching away from home](/help/remote-tv)) and **Simulcast** (see [Simulcast](/help/simulcast));
- the **Plex** panel, the addresses for Plex and other players, and the steps to add Maktaba to Plex by hand.

Only admins can open this page. The channels themselves are built on [Channels](/help/channels).

## How to use it {#how-to-use}

### The tuner {#tuner}

- Turn on **Tuner on: Plex can find and tune Maktaba**. While it is off, Maktaba is not advertised to Plex.
- **Tuner name** is the name players show. **Tuners** is how many channels Plex may watch or record at the same time.
- Click **Save**. Entering the address by hand works straight away; automatic discovery on the network starts the next time Maktaba restarts.

### The addresses {#addresses}

**Addresses for Plex** lists three addresses, each with a **Copy** button:

- **HDHomeRun address**: what Plex, Jellyfin and Emby take as the tuner.
- **XMLTV guide**: the programme guide.
- **M3U playlist**: the channel list for VLC and other IPTV players.

### Add Maktaba to Plex {#add-to-plex}

The quickest way is the **Plex** panel (below). To do it by hand in Plex (**Add it in Plex**):

1. Turn the tuner on above.
2. In Plex, open Settings → Live TV & DVR → Set up Plex DVR.
3. If Plex does not find Maktaba (some networks and Wi-Fi access points block the discovery broadcast), choose to enter the address manually and type the **HDHomeRun address**.
4. When Plex asks for a guide, pick the XMLTV option and paste the **XMLTV guide** address.
5. Check the channel list and finish. The channels then appear under Live TV.

More about Plex is in [Plex](/help/plex).

### The Plex panel {#plex-panel}

When the Plex extension is on and signed in, this panel shows the **Plex server** (reachable or not), **Maktaba in Plex** (added or not yet), the **DVRs in Plex**, how many **Channels in Plex** are mapped, and when the guide and channel list were last sent.

- **Add Maktaba to Plex** adds Maktaba's tuner and guide to Plex's Live TV & DVR, after showing exactly what it changes. Other tuners in Plex stay as they are.
- **Refresh Plex now** sends the channel list and reloads the guide in Plex at once.
- Under **Automatic** you can let Maktaba add itself when Plex has no DVR for it, refresh the guide when the schedule changes, and refresh the channel list when channels change. **Guide reload at most every (minutes)** limits reloads caused by the schedule growing.

The addresses Plex is given, and the guide's language and name, are in [Settings → Rules](/settings/rules) under Plex Live TV (see [Rules](/help/rules#plexlive)).

### Other players {#other-players}

Jellyfin and Emby take the same HDHomeRun address and XMLTV guide. VLC and other IPTV players can open the M3U playlist. Plex viewers away from home use Plex's own remote access: Plex pulls the channel from this tuner and serves it, and Maktaba sends nothing to Cloudflare for them.

### Streaming machines {#streaming-hosts}

**Streaming hosts** lists the machines that encode channels. The home always streams. Another machine of your cluster can stream too:

1. Join the machine to the home first (see [Machines](/help/machines)). It must be able to read the library.
2. Under **Cluster settings**, turn on **Other machines may stream**.
3. On that machine's card, turn on **Stream on this machine**.

Each card shows the machine's role (**Home** or **Machine**), its state, **Channels** in use of its capacity, **Encoders**, **Load**, **Last heartbeat**, **Version** and **Stream address**. You can set:

- **Capacity**: how many channels at once, 0 to 64. Empty means worked out from the machine's encoders.
- **Weight**: 1 to 100; a bigger weight counts as more room.
- **Stream base URL**: where players reach this machine. Empty means the address it reports.

Click **Save** on the card after changing these.

### Where channels go {#placement}

A channel is placed on a machine when its first viewer tunes in, and it stays there while people watch. **Where channels go** in **Cluster settings** chooses how:

- **Home first**: the home until it is full (see **Home full at (%)**), then the machine with the most room.
- **Balanced**: always the machine with the most room.

The other cluster settings (failover, when a machine counts as down) are in [Settings → Rules](/settings/rules) under Maktaba TV machines (see [Rules](/help/rules#tv-cluster)).

### Prefer a machine for a channel {#prefer-host}

In the channel editor, **Basics** → **Prefer host** picks the machine that should encode that channel while it is up and has room. **Automatic** lets Maktaba choose. It is saved at once.

### Failover {#failover}

If a streaming machine stops answering, it is marked **Down** and, with failover on, its channels move to another machine and carry on at the right point of their schedule. Browser viewers see a short stall. The **TV** line on [System health](/help/system-health) counts machines online, channels on air and failovers in the last 24 hours.

### Maintenance: drain a machine {#drain}

Before you restart or update a streaming machine, click **Drain** on its card. It takes no new channels; its channels stay until nobody watches them. **Move now** moves them all at once (viewers are interrupted for a few seconds). Click **Stop draining** when the machine is back.

### On air {#on-air}

**On air** lists every channel running now: its **Host**, **State**, **Encoder**, viewers (**HLS viewers**, **Tuners**, **Remote viewers**), **Since**, **Restarts**, **NVENC fallbacks** and **Push** (whether it is sent on for remote viewing or simulcast).

### Move or stop a channel {#move-channel}

- **Move** opens a dialog: pick the machine under **Move to**, or **Automatic** for the best one, then click **Move**. Viewers of the channel are interrupted for a few seconds.
- **Stop** stops the channel everywhere. It starts again when someone tunes in.

## What the states and messages mean {#states}

### Machine states {#host-states}

- **Online**: running and taking channels.
- **Down**: it stopped answering. Its channels move elsewhere when failover is on.
- **Draining**: takes no new channels; its channels leave when idle or when you click **Move now**.
- **Switched off**: **Stream on this machine** is off, or other machines are not allowed to stream.
- **Not fit**: it cannot take channels. The card says why ("Not taking channels: …"), for example it cannot read the library, it runs another version than the home, or it went down twice in a short time.

### On air states {#on-air-states}

- **Starting**: the channel is being placed on a machine.
- **Live**: the channel is encoding and on the air.
- **Moving**: the channel is moving to another machine.
- **Running**: the channel is running on the TV process.
- **Stale**: the information about this channel is out of date.

### Notices {#notices}

- **Two homes**: two machines both say they are the home. Only one may be.
- **No room**: no machine has room for a channel someone wants to watch.
- **Preferred machine skipped**: a channel's preferred machine was down or full, so another one took it.

### Messages {#messages}

- **Tuner turned on.** / **Tuner turned off.** / **Tuner settings saved.**: your change was saved.
- **Sent to Plex.**: Maktaba asked Plex to update; **Maktaba in Plex** updates by itself when Plex answers.
- **Plex is loading the guide**: channels and programmes show up in Plex in a few minutes; refreshing meanwhile does not speed it up.
- **Waiting: …**: the Plex task is waiting; the reason is shown.

## FAQ {#faq}

### Plex does not find Maktaba. What now? {#faq-plex-not-found}

Some networks and Wi-Fi access points block the discovery broadcast. In Plex's setup, choose to enter the address manually and type the **HDHomeRun address** from this page.

### How many people can watch at once? {#faq-tuners}

**Tuners** limits how many channels Plex may watch or record at the same time. Browser viewers of the same channel share one encode, so the limit on channels is each machine's capacity.

### Do I need a second machine? {#faq-second-machine}

No. The home streams every channel by itself. Add a streaming machine only when the home has no room for the channels people watch at once.

### Does moving a channel interrupt viewers? {#faq-move}

Yes, for a few seconds. They carry on at the right point of the schedule.

### Where do I make remote viewing or simulcast work? {#faq-remote}

On this page too. See [Watching away from home](/help/remote-tv) and [Simulcast](/help/simulcast).

## When something goes wrong {#troubleshooting}

### Could not load the Live TV settings {#load-error}

- **What it means:** the page could not read the tuner settings.
- **Does Maktaba fix it itself?** No.
- **What you can do:** reload the page. If it keeps failing, check [System health](/help/system-health).

### These addresses are guessed from this page's address {#addresses-guessed}

- **What it means:** Maktaba has no public address set, so it built the addresses from the address you used to open this page. Other devices may not reach that address.
- **Does Maktaba fix it itself?** No.
- **What you can do:** open this page from the address your TV devices use, or ask whoever set up the server to set the server's public address.

### Copying is blocked on this address {#copy-blocked}

- **What it means:** the browser does not allow copying here.
- **Does Maktaba fix it itself?** It selects the text for you.
- **What you can do:** press Ctrl+C (or long-press) to copy it.

### Turn on the Plex extension and sign in with Plex first {#plex-not-configured}

- **What it means:** the Plex panel needs the Plex extension.
- **Does Maktaba fix it itself?** No.
- **What you can do:** click **Open Extensions**, turn on Plex and sign in. See [Plex](/help/plex).

### Plex server not reachable {#plex-unreachable}

- **What it means:** Maktaba could not talk to your Plex server at the last check.
- **Does Maktaba fix it itself?** It checks again later.
- **What you can do:** make sure Plex is running, then click **Refresh Plex now**. See [Plex unreachable](/help/failures#plex-unreachable).

### Maktaba is in a DVR of its own {#separate-dvr}

- **What it means:** an earlier add put Maktaba in a separate DVR that Plex's Live TV & DVR page does not show.
- **Does Maktaba fix it itself?** No.
- **What you can do:** click **Add Maktaba to Plex**. It moves Maktaba into the DVR Plex shows, next to the other tuners.

### A machine is Down {#host-down}

- **What it means:** the streaming machine stopped answering.
- **Does Maktaba fix it itself?** Yes, with failover on: its channels move to another machine.
- **What you can do:** check the machine is on and connected (see [Machines](/help/machines)). It takes channels again once it is back online.

### A machine is Not fit {#host-unfit}

- **What it means:** it cannot take channels; the card says why.
- **Does Maktaba fix it itself?** A machine held out after going down twice is let back in after a while. The other reasons need you.
- **What you can do:** if it cannot read the library, fix its storage on [Machines](/help/machines); if its version differs from the home, update it.

### Two homes {#two-homes}

- **What it means:** two machines both say they are the home.
- **Does Maktaba fix it itself?** No.
- **What you can do:** fix the setup of one of them so only one is the home.

### No room {#no-room}

- **What it means:** every streaming machine is full, so a channel could not start. Viewers see it as unavailable, and Plex says all tuners are in use.
- **Does Maktaba fix it itself?** The channel starts when a machine has room again.
- **What you can do:** raise a machine's **Capacity**, let another machine stream, or stop channels nobody needs.

### Preferred machine skipped {#pin-skipped}

- **What it means:** a channel's **Prefer host** was down or full, so it went elsewhere.
- **Does Maktaba fix it itself?** Yes: the channel still plays on another machine.
- **What you can do:** nothing, unless the preferred machine stays unavailable; then check it or set the channel back to **Automatic**.

### Could not refresh the streaming hosts {#stale-view}

- **What it means:** the latest read failed, so the page shows the last view it had.
- **Does Maktaba fix it itself?** It keeps trying.
- **What you can do:** wait a moment; reload the page if it does not clear.
