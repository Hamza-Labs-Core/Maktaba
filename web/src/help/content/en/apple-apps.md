# The iPhone, iPad and Mac apps

Maktaba Player and Maktaba Remote on iPhone, iPad and the Mac: what each one does, how to connect it to your home, and the one permission they ask for.

## What it is {#what-it-is}

There are two apps, each talking to your Maktaba home over its address:

- **Maktaba Player** (iPhone, iPad and Mac): browse your home's libraries and watch. On iPhone and iPad its name under the icon is **Maktaba**; on the Mac it is **Maktaba Player**.
- **Maktaba Remote** (iPhone and iPad): run your home from your phone. It opens on what needs attention and can retry failed work. Only the home's admins can use it; anyone else is told to use Maktaba Player.

Both keep your sign-in for each home in the device's Keychain and remember the homes you have used.

## How to use it {#how-to-use}

1. Open the app. The first time, it shows **Before you start**: the permission it needs and why (see [Permissions](#permissions)). Read it, tap **Allow access**, then **Continue**.
2. Pick your home. **On this network** lists the homes the app finds by itself. You can also type the home's address, like `192.168.1.20:8088` or `mac.local:8088` (the address you open the web app at), or, on iPhone and iPad, tap **Scan a QR code** and point the camera at the code on the home's **Connect an app** page (account menu in the web app). On the Mac, paste the link from that page.
3. Sign in with your Maktaba user name and password, or choose **With a code**: the app shows a short code and a QR code. On a device that is already signed in, open the QR (or the web app's **Sign in a device** page, or **Approve a sign-in** in the account menu of the Player or the Remote), check which device is asking, and tap **Approve**. The new device gets your account, never more.
4. Next time the app opens on that home. **Recent homes** lists every home you have added; rename one or swipe it to forget it. A sign-in belongs to the address it was made at: the same home at a new address asks you to sign in again.

A `maktaba://connect` link opened from elsewhere always asks before it adds or switches a home.

### Permissions {#permissions}

The apps ask for Local Network, and on iPhone and iPad the camera only when you scan a QR code. They ask for no microphone, location, photos, contacts or notifications.

**Local Network (required)**

- **What it is:** permission to talk to devices on your Wi-Fi or Ethernet. iOS and iPadOS (since 14) and macOS (since 15) block it until you allow it, app by app.
- **Why the apps need it:** your home is usually on the same network as your phone or Mac, at an address like `192.168.1.20:8088` or a `.local` name. The apps also look for homes that announce themselves on the network (Bonjour, `_maktaba._tcp`), which is how they check whether access is allowed.
- **How to grant it:** on the first run, **Before you start** shows **Local Network** as **Not asked yet**. Tap **Allow access**: the system asks once, and you choose **Allow**. The card then shows **Allowed**.
- **Without it:** the app can't reach a home on your own network: connecting fails or waits. A home you reach over the internet (an `https://` address outside your network) still works.
- **If you said no:** the card shows **Not allowed** and a button that opens the right place. You can also go there yourself:

  - **iPhone and iPad:** Settings → Privacy & Security → Local Network, then turn on **Maktaba** (Player) or **Maktaba Remote**. The same switch is on the app's own page: Settings → Apps → Maktaba → Local Network. **Open Settings** in the app opens that page.
  - **Mac:** System Settings → Privacy & Security → Local Network, then turn on **Maktaba Player**. **Open System Settings** in the app opens that pane.

**Camera (optional, iPhone and iPad)**

- **Why:** to scan a home's QR code or a sign-in code's QR. Nothing else; no picture is kept or sent.
- **When it is asked:** the first time you tap **Scan a QR code**, not at the first start.
- **Without it:** type the address or the code instead; everything else works.
- **To change it:** Settings → Apps → Maktaba (or Maktaba Remote) → Camera.


  Come back to the app: it checks again by itself and shows **Allowed**.

Playing sound with the screen locked (iPhone and iPad) is part of the Player and needs no permission.

## What the states and messages mean {#states}

The **Before you start** sheet shows each permission's state:

- **Allowed**: the app may use the local network.
- **Not allowed**: you turned it off, or answered **Don't Allow**. The sheet comes back each time the app starts until it is allowed; tap **Not now** to close it for now.
- **Not asked yet**: the app has not asked. Tap **Allow access**.
- **Can't tell**: the check got no answer (no Wi-Fi, or it took too long). Join your home's network and tap **Check again**.
- **Checking…**: the app is checking right now.
- **Required**: the app can't reach a home on your network without it.

**For the home's admins** (Maktaba Remote): you signed in as a user who is not an admin. Use Maktaba Player instead, or sign out and sign in as an admin.

## FAQ {#faq}

**Why does the app need my local network?** Your home is a computer on your network. Without this permission the system blocks the app from reaching it.

**Can I change my answer later?** Yes, at any time, in the Settings paths under [Permissions](#permissions). The app notices when you come back to it.

**Does the Apple TV app ask for the same?** No. Apple TV has no Local Network permission, so the TV app asks for nothing.

**I closed the sheet. How do I see it again?** It shows by itself whenever Local Network is not allowed, on the next start. When everything is allowed, there is nothing to show.

## When something goes wrong {#troubleshooting}

- **"Connect" spins or fails with a home on my network:** check Local Network is on (see [Permissions](#permissions)). Then check the address: open it in Safari on the same device; the web app should load.
- **The switch for the app is not in Settings → Privacy & Security → Local Network:** the list only has apps that have asked. Open the app and tap **Allow access** once, then look again.
- **It says Allowed but I still can't connect:** the phone may be on mobile data or a guest Wi-Fi that keeps devices apart. Join the same network as your home.
- **On the Mac, Maktaba Player is listed twice in Local Network:** you have two copies installed (for example an old build). Turn it on for both, or delete the copy you don't use.
- **Can't tell keeps showing:** you are probably not on a Wi-Fi or Ethernet network. Join one and tap **Check again**.
