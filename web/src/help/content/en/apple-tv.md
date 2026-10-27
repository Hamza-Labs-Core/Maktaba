# The Apple TV app

Maktaba on Apple TV: your home's libraries on the television, driven with the Siri Remote. How to find your home, sign in with a code, and why it asks for no permissions.

## What it is {#what-it-is}

**Maktaba** for Apple TV (tvOS 26) shows your home's rails (continue watching first), its libraries and search, and plays videos with the system player: scrubbing, audio and subtitle choices. Settings shows who is signed in and to which home, the theme, and signing out.

## How to use it {#how-to-use}

1. Open **Maktaba** on the Apple TV. **Choose your home** shows:
   - **Recent homes**: the homes this Apple TV used, the last one first. Hold the select button on one to **Rename** it (on this TV only; leave the name empty to use the home's own) or **Remove** it (and its sign-in).
   - **On this network**: the homes that announce themselves on this network, one row per home. A machine that only works for a home is not listed.
   - **By address**: type the address you open Maktaba at in a browser (like `192.168.1.20:8088`) and choose **Connect**. A `maktaba://connect` link the home shared works too. The TV first checks that a Maktaba home answers there.
2. Sign in. The TV shows **Sign in to** your home, a code in large letters and a QR code. On a phone or computer signed in to Maktaba, scan the QR code, or open **Approve a sign-in** (in Maktaba Remote, or the Player's account menu) and enter the code, or open the page under **Or open this page there:**. Check what asks, then **Approve**. The TV is signed in as you, with your own role. **A new code in 9:55** counts down; a code that runs out is replaced by itself.
3. Or choose **Username & password** and type them with the Siri Remote. **Sign in with a code** goes back to the code. A home that signs in with a password only shows the password straight away.
4. **Choose another home** goes back to the list. Browse with the Siri Remote. On a video's page, **Resume** starts where you stopped.

### A link to a home {#link}

When something opens a `maktaba://connect` link on the Apple TV, the app asks **Connect to this home?** with the home's name and addresses, before it contacts anything. **Cancel** has the focus; choose **Connect** only if you trust where the link came from. You sign in there yourself.

### Permissions {#permissions}

The Apple TV app asks for **no permissions**. tvOS has no Local Network permission (unlike iPhone, iPad and the Mac), so the TV can find and reach your home on the network without asking, and there is nothing to allow or turn on in Settings. It uses no camera, microphone, location or notifications.

There is no permissions walkthrough on Apple TV for the same reason: the app opens straight on **Choose your home**.

If a later version needs a permission, the app shows **Before you start** on its first run, with each permission, why it is needed and a button for it, as on the [iPhone, iPad and Mac apps](/help/apple-apps#permissions).

## What the states and messages mean {#states}

- **Looking for homes on this network…**: the TV is listening for homes that announce themselves on the network. A home appears within a few seconds; if none does, type its address.
- **Connecting…**: the TV is checking that a Maktaba home answers at the address.
- **Could not reach the home: …**: nothing answered at that address (off, another network, a wrong port).
- **Something answered at that address, but it is not a Maktaba home.**: check the address and port.
- **That is not a home's address.**: what was typed is not an address.
- **Waiting for approval…**: the code is shown and the TV is waiting for someone to approve it on a phone or computer. Once approved, it signs in.
- **This home signs in with a username and password only.**: an older home, or one without sign-in by code.
- **Resume from 12:34**: you watched part of this video; it starts there.

## FAQ {#faq}

**Do I need to allow anything in the Apple TV's Settings?** No. There is no permission to grant.

**Why does my iPhone ask about the local network and the Apple TV doesn't?** iPhone, iPad and the Mac ask before an app may talk to devices on your network; Apple TV doesn't have that question.

**Can I sign in without typing a password?** Yes: use the code, and approve it from a phone or computer that is signed in.

## When something goes wrong {#troubleshooting}

- **My home isn't listed under On this network:** check the Apple TV and the home are on the same network (not a guest Wi-Fi). Some routers block the announcements between Wi-Fi and Ethernet; type the address under **By address** instead.
- **Typing the address fails:** open the same address in a browser on another device on the network; the web app should load. Check the port (usually `8088`).
- **The code never gets approved:** approve it while signed in on the home itself, not through Maktaba Cloud. A code that expires is replaced by a new one on the TV by itself.
