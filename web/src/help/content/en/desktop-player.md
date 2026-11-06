# Maktaba Player for Windows and Linux

Watch your home on a Windows or Linux computer, in a window or across the room in big screen.

## What it is {#what-it-is}

**Maktaba Player** is the desktop app for watching, with the sections of the phone and Mac Players: Home, Libraries, Live TV, Search and Profile. It does not run a home (that is the **Maktaba** app, which carries the server), and it does not manage one: processing, settings and users stay on the home's web page.

It runs on Windows 10 and later (x86-64) and on Linux (x86-64 and ARM64, as a `.deb` or an `.rpm`). On a Mac, use the Apple app instead (see [The Apple apps](/help/apple-apps)). It is in English and Arabic; Arabic is laid out right to left.

## How to use it {#how-to-use}

### Install it {#install}

Download it from the release page of Maktaba:

- **Windows**: `Maktaba-Player_<version>_x64.msi`. It installs for you alone, so it needs no administrator, and it adds Maktaba Player to the Start menu.
- **Linux**: `maktaba-player_<version>_amd64.deb` (or `_arm64.deb`) on Debian and Ubuntu, `maktaba-player-<version>.x86_64.rpm` (or `.aarch64.rpm`) on Fedora and openSUSE. Open it with your software installer, or `sudo apt install ./maktaba-player_<version>_amd64.deb`. To play video it needs the system's FFmpeg libraries (the `libavcodec` package; most desktops have them).

### Choose a home and sign in {#choose-home}

1. Open Maktaba Player. **On this network** lists the homes that announce themselves on your network; **Your homes** lists the ones this computer used before (**Rename** gives one a name on this computer only; **Remove** forgets it and its sign-in).
2. If yours is not listed, type its address under **Add a home**, like `192.168.1.20:8088`, or paste the link that [Connect an app](/account/connect) copies (`maktaba://connect?…`), then **Connect**.
3. Sign in with your **Username** and **Password**, or choose **With a code**: the player shows a code and a QR code. On a phone or computer already signed in to the home, open [Sign in a device](/pair) (or scan the QR code), check what is asking, and press **Approve**. A new code appears when one runs out.

The player stays signed in to each home until you sign out (**Profile → Account → Sign out**, or **Account → Sign out** in the menu). To use another home, choose **Account → Switch home**, or **Choose another home** on the sign-in screen.

### Through Maktaba Cloud {#through-cloud}

Away from home, or for a home on another network, open it through Maktaba Cloud. Your account on the home must be linked to your Maktaba Cloud account first, from the home's web page (see [Linked accounts](/help/account#linked-accounts)).

1. In the home picker, press **Through Maktaba Cloud**.
2. Sign in with your Maktaba Cloud **Email** and **Password**. (**Advanced** sets another **Maktaba Cloud address**, for a test or your own Maktaba Cloud; leave it empty for the usual one.)
3. **Your homes** lists the homes linked to your cloud account, **Online** or **Offline**. A home marked **Not linked to your account** cannot be opened: link it on that home first. Owning a home in Maktaba Cloud is not an account in it.
4. Choose a home. The app reaches it at its own address when you are on its network, otherwise through Maktaba Cloud, and signs you in there without your password.

The home decides what you can do: you are your own account there, and through the cloud only watching, unless you are an admin of the home who allowed managing for your own link. The app keeps both of the home's addresses and uses whichever answers. A sign-in belongs to the address it was made at, so at the other address the app signs in again, through the cloud when the cloud gave that address and you are signed in to it, otherwise with your password or a code. The cloud sign-in lasts until the app quits; **Sign out of Maktaba Cloud** ends it sooner. Signing out of the cloud does not sign you out of your homes.

### The sections {#sections}

After you sign in, the sections are on a rail at the side of the window (on the right in Arabic): **Home**, **Libraries**, **Live TV**, **Search** and **Profile**. Choosing a video, a series, a library or a channel opens its page in the section you are in; **Back** (above the page), **Alt+Left** (**Alt+Right** in Arabic) or your mouse's back button return to the page before. Choosing a section's name again takes it back to its first page. Each section keeps its place while you look at another.

### Home {#home}

Home opens with a featured video or a new series (**Play** or **Resume**, and **More options** for its page), then rows: **Continue Watching**, **On Deck** (the next lesson of a series), **What's On Now** (Live TV), **Favorites**, **Recently Added**, **Recently Watched**, a row for each library you pinned, and **For You**. A card in Continue Watching plays from where you left it; any other card opens its page; **See all** opens the whole library. On a For You card, right-click (or **More options**) and choose **Not interested**: the card leaves and is not suggested again. Home reads the rows again when you come back to it after five minutes.

### Libraries {#libraries}

**Libraries** lists the libraries you may watch, the pinned ones first under **Pinned**, and **All videos**. The pin on a card (or a right-click) pins or unpins it; a pinned library also gets its own row on Home. A library's page shows its rows (On Deck, Continue Watching, Recently Added, Recently Watched, its series), then **Videos**, with **Sort** (Newest, A–Z) and filters by **Language** and **Speaker**, and **Series**. More videos appear as you scroll to the end.

### A series and a video {#video-page}

A series' page shows its seasons and episodes, **Continue watching** (the first episode you have not watched, from where you left it), and **Mark watched** on each episode or on the whole season. A video's page shows its details, **Play** (or **Resume from** and **Play from start** when you stopped part way), **Favorite**, **Mark watched**, its **Chapters** (each plays from its start), the **Next episodes** of its series and the playlists it is in.

### Search {#search}

**Ctrl+F** (or **/**) opens Search from anywhere. Type: the words you searched before are suggested (**Up** and **Down** choose, **Enter** searches). Results come in two groups: **Titles**, and **In the transcript**, the words said in a video, each passage with **Play from** its moment. **Show more** gives the next results. With the field empty you see your **Recent searches**: remove one, or **Clear all**. On a wide window, choosing a result shows its page beside the results.

### Watch {#watch}

A video resumes where you left it, unless you finished it or were nearly at the end; **Play from start** starts it over. The controls are under the picture: play and pause, back and forward 10 seconds, the bar (with a mark at each chapter), the volume, **Subtitles**, **Audio**, **Quality** and full screen.

- **Subtitles** shows the video's subtitles over the picture, in the language you choose, or **Off**. **Audio** chooses another sound track when the video has one. Your choice carries over to the next video when it has that language.
- **Quality** is **Auto** or a height (1080p, 720p…); the video goes on from the same moment.
- **Up next**: near the end of an episode, a card offers the next one with a countdown; **Play now** starts it, **Cancel** stays. A playlist plays its videos in order the same way.

The keys:

| Key                           | Does                                                    |
| ----------------------------- | ------------------------------------------------------- |
| **Space**, the media play key | play or pause                                           |
| **←** / **→**                 | back or forward 10 seconds (in Arabic, **→** goes back) |
| **Shift+←** / **Shift+→**     | back or forward 30 seconds                              |
| **N**                         | the next episode                                        |
| **S**                         | subtitles on or off                                     |
| **M**                         | mute                                                    |
| **↑** / **↓**                 | volume                                                  |
| **F**                         | full screen (**F11** stays big screen)                  |
| **Escape**                    | leave full screen, else close the video                 |

Where you stopped is kept: Continue Watching on your other devices starts there. The home sends this player H.264 video, at most 1080p. A video stored another way (HEVC, a 4K file) is converted by the home as it plays, so it may take a few seconds to start.

### Live TV {#live-tv}

**Now** lists the channels you may watch, with what is on and what is next. **Guide** shows them by time: **Earlier** and **Later** move it by two hours; the wheel scrolls the channels and **Shift**+wheel the time. A channel opens its player: the picture with **LIVE**, what is on now and next, and the rest of the day. **Pause** stops the picture and **Back to live** returns to what is airing; **Previous channel** and **Next channel** (also **Page Up** / **Page Down** and **↑** / **↓**) change channel; **Escape** closes it. Away from home (through Maktaba Cloud) the channel comes through a remote session the home allows; leaving the channel ends it.

### Profile {#profile}

- **Account**: who you are on which home, your Maktaba Cloud link, **Switch home** and **Sign out** (it asks first).
- **Watch history**: what you watched; **Remove** takes a video out, and out of Continue Watching. **Favorites** and **Playlists** (a playlist's **Play** plays it in order).
- **Sessions**: the browsers signed in to the home as you; **Sign out** ends one you do not recognise.
- **Approve a TV or device**: the same as **Account → Approve a sign-in**.
- **Playback**: subtitles on by default, the subtitle and audio languages, and the quality a video starts at.
- **Language** and **Help and about** (the player's and the home's versions, help, **Check for updates**).

### A feature that is off {#feature-off}

An admin can turn parts of the home off on its Components page (Live TV, transcripts, subtitles, search by meaning, chapters, recommendations). The player follows what the home has on, checking every few minutes. For a viewer, a feature that is off is simply not there: no Live TV section, no For You row, no transcript results, no chapters, no Subtitles control. An admin sees a sentence in its place, like **Live TV is off on this home.**, with **Turn it on in Components**, which opens that page in the browser.

### Big screen {#big-screen}

For a computer under the TV: **Big screen → Big screen**, or **F11**, fills the screen and makes everything larger. Move with the **arrow keys**, open with **Enter**, go back with **Escape**. **F11** again leaves it. Tick **Start in big screen** to open the player in it every time.

### Approve a sign-in {#approve}

**Account → Approve a sign-in** signs in another device as you: type the code it shows, check the app, the device and its name, then press **Approve**. The device gets your account and your access, nothing more.

### Language {#language}

**Language** (in the menu, or in Profile) switches between English and العربية at once; the player remembers it.

### Updates {#updates}

**Help → Check for updates** asks for the newest release of the player. Before anything is installed, the download's signature is checked against Maktaba's key; a download that does not check is deleted.

- **Windows**: **Download and install** runs the installer, and the player closes to update.
- **Linux**: the checked package is saved, and the player says where; install it with your software installer.

## What the states and messages mean {#states}

- **Looking for homes on this network…**: nothing has answered yet. Homes announce themselves every few seconds; a network that blocks this (some offices and guest Wi-Fi) shows none, so type the address.
- **No Maktaba home answered at …**: nothing at that address is a Maktaba home, or it did not answer. Check the address and that the home is on.
- **The username or the password is wrong.**: try again, or sign in with a code.
- **This home has no sign-in by code. Use your password.**: the home is older than sign-in by code.
- **The home did not answer: …**: the player kept your sign-in but could not reach the home. Press **Try again**, or choose another home.
- **The video could not play: …**: the home could not start the video, or the network dropped. Close it and play it again.
- **This code is not waiting for a sign-in.** / **This code was already used.**: ask the device for a new code.
- **A sign-in through Maktaba Cloud cannot approve a device.**: approve it from a sign-in on the home's own address.
- **You have the latest version (…)**: there is no newer player.
- **Couldn't refresh — showing the last rows.**: Home could not be read again; what you see is from the last time. It tries again when you come back.
- **Could not tell the home. Try again.** / **Couldn't save that. Try again.** / **That did not work**: a change (Not interested, a pin, a favorite, watched, a removal) did not reach the home, so it was put back.
- **Text matches only: the meaning search did not answer.**: the results match your words; search by meaning will be back later. **Searching by words only. Search by meaning is off.** (an admin): it is off on the home.
- **Nothing matches “…”.**: try other words.
- **Not available** / **This video isn't available**: it was removed, or it is not shared with you.
- **Tuning…**: the channel is starting. **This channel can't play**, with why: **You're already watching on as many devices as allowed.**, **This home's remote viewing allowance is used up.**, **You can't watch away from home.**, **Watching away from home isn't available right now.**, **The channel took too long to start.** or **This channel isn't available.** **The remote session ended.**: the home ended watching away from home; open the channel again.
- **Live TV isn't available on this home.**: Live TV was turned off while you watched.
- **Changing…**: the player is switching the audio or the quality; if it cannot, the video goes on as it was and says why.
- **This build has no update key: update it by hand.**: a build made without Maktaba's update key cannot check an update's signature, so it installs none; download the new version from the release page.

## FAQ {#faq}

### Why is there no Live TV section? {#faq-no-live}

The home has no channel you may watch, or Live TV is off on it (see [A feature that is off](#feature-off)). The sections came with version 0.3.83 of the player; an older one shows a list of libraries and a grid instead (**Help → Check for updates**).

### Can I manage my home from the player? {#faq-manage}

No. The player only watches. Open the home's web page to manage it; an admin's account does not change what the player does.

### Where does the player keep my sign-in? {#faq-tokens}

On this computer, sealed: on Windows with the system's protection for your Windows account, on Linux with a key in a file only your user can read. Your password is never kept. **Remove** on a home forgets its sign-in.

### Does the video go through the internet? {#faq-network}

No. The player talks to the home at the address you chose. When that address is the home's address through Maktaba Cloud, the video goes through the cloud.

## When something goes wrong {#troubleshooting}

### The player shows no home on this network {#no-homes}

- **What it means:** no home answered the player's search. The network may block it, or the computer's firewall may.
- **Does Maktaba fix it itself?** No.
- **What you can do:** type the home's address under **Add a home**. On Windows, allow Maktaba Player through the firewall on private networks.

### The video stays black or does not start on Linux {#linux-video}

- **What it means:** the system's FFmpeg libraries, which the player uses to decode video, are missing.
- **Does Maktaba fix it itself?** No.
- **What you can do:** install them (`sudo apt install libavcodec-extra` on Debian and Ubuntu, `sudo dnf install ffmpeg-libs` on Fedora with RPM Fusion), then open the video again.

### No update was installed: … {#update-failed}

- **What it means:** the update could not be downloaded, or its signature did not check, so nothing was installed.
- **Does Maktaba fix it itself?** No; the player keeps working on its version.
- **What you can do:** try again later, or download the new version from the release page.
