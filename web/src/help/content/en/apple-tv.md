# The Apple TV app

Maktaba on Apple TV: your home's libraries on the television, driven with the Siri Remote. How to find your home, sign in with a code, and why it asks for no permissions.

## What it is {#what-it-is}

**Maktaba** for Apple TV (tvOS 26) shows your home's rails (continue watching first), its libraries and search, and plays videos with the system player: scrubbing, audio and subtitle choices. Settings shows who is signed in and to which home, the theme, and signing out.

## How to use it {#how-to-use}

1. Open **Maktaba** on the Apple TV. **Choose your home** lists the homes it finds **On this network**. Pick yours, or type its address under **Another home** (like `192.168.1.20:8088`).
2. Sign in. The TV shows a code and a QR code: on a phone or computer signed in to Maktaba, scan it or open the address shown and enter the code, then approve. The TV is signed in as you, with your own role. You can also sign in with your user name and password.
3. Browse with the Siri Remote. On a video's page, **Resume** starts where you stopped.

### Permissions {#permissions}

The Apple TV app asks for **no permissions**. tvOS has no Local Network permission (unlike iPhone, iPad and the Mac), so the TV can find and reach your home on the network without asking, and there is nothing to allow or turn on in Settings. It uses no camera, microphone, location or notifications.

There is no permissions walkthrough on Apple TV for the same reason: the app opens straight on **Choose your home**.

If a later version needs a permission, the app shows **Before you start** on its first run, with each permission, why it is needed and a button for it, as on the [iPhone, iPad and Mac apps](/help/apple-apps#permissions).

## What the states and messages mean {#states}

- **Looking for homes…**: the TV is listening for homes that announce themselves on the network. A home appears within a few seconds; if none does, type its address.
- **Waiting for approval…**: the code is shown and the TV is waiting for someone to approve it on a phone or computer. Once approved, it signs in.
- **Resume from 12:34**: you watched part of this video; it starts there.

## FAQ {#faq}

**Do I need to allow anything in the Apple TV's Settings?** No. There is no permission to grant.

**Why does my iPhone ask about the local network and the Apple TV doesn't?** iPhone, iPad and the Mac ask before an app may talk to devices on your network; Apple TV doesn't have that question.

**Can I sign in without typing a password?** Yes: use the code, and approve it from a phone or computer that is signed in.

## When something goes wrong {#troubleshooting}

- **My home isn't listed under On this network:** check the Apple TV and the home are on the same network (not a guest Wi-Fi). Some routers block the announcements between Wi-Fi and Ethernet; type the address under **Another home** instead.
- **Typing the address fails:** open the same address in a browser on another device on the network; the web app should load. Check the port (usually `8088`).
- **The code never gets approved:** approve it while signed in on the home itself, not through Maktaba Cloud. If it expires, go back and start again for a new code.
