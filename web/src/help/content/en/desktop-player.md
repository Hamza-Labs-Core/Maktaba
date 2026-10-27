# Maktaba Player for Windows and Linux

Watch your home on a Windows or Linux computer, in a window or across the room in big screen.

## What it is {#what-it-is}

**Maktaba Player** is the desktop app for watching: browse your home's libraries, search, and play. It does not run a home (that is the **Maktaba** app, which carries the server), and it does not manage one: processing, settings and users stay on the home's web page.

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

The player stays signed in to each home until you sign out (**Account → Sign out**). To use another home, choose **Account → Switch home**, or **Choose another home** on the sign-in screen.

### Watch {#watch}

Pick a library on the left, or **All videos**, and a video. **Search** looks through the titles. **Load more** shows the next videos.

While a video plays:

- **Space** plays and pauses; the **arrows** go back or forward 10 seconds (in Arabic, the arrows follow the reading direction).
- Drag the bar to move in the video.
- **Escape**, or the back arrow, closes the video. Where you stopped is kept: Continue Watching on your other devices starts there.

The home sends this player H.264 video, at most 1080p. A video stored another way (HEVC, a 4K file) is converted by the home as it plays, so it may take a few seconds to start.

### Big screen {#big-screen}

For a computer under the TV: **Big screen → Big screen**, or **F11**, fills the screen and makes everything larger. Move with the **arrow keys**, open with **Enter**, go back with **Escape**. **F11** again leaves it. Tick **Start in big screen** to open the player in it every time.

### Approve a sign-in {#approve}

**Account → Approve a sign-in** signs in another device as you: type the code it shows, check the app, the device and its name, then press **Approve**. The device gets your account and your access, nothing more.

### Language {#language}

**Language** switches between English and العربية at once; the player remembers it.

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
- **This build has no update key: update it by hand.**: a build made without Maktaba's update key cannot check an update's signature, so it installs none; download the new version from the release page.

## FAQ {#faq}

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
