# The Android TV app

Maktaba for Android TV and Google TV: your home's libraries on the big screen, driven with the remote.

## What it is {#what-it-is}

Maktaba for Android TV is a ten-foot app for Android TV and Google TV (Android 9 or later). You browse your home's libraries, search and watch, all with the remote's arrows and its centre button. It shows on the TV's home row as **Maktaba**.

## How to use it {#how-to-use}

### Connect and sign in {#connect}

1. Open **Maktaba** from the TV's home screen. On Android 17 and later, the first time, it shows **Before you start** (see [Permissions](/help/android-tv#permissions)).
2. **Choose your home**: homes on this network appear by themselves. If yours is not listed, choose **Enter an address** and type it, like `192.168.1.20:8088`.
3. **Sign in to** your home with a code: the TV shows a code and a QR code. On your phone or computer, signed in to Maktaba, open the address the TV shows (or scan the QR code), check that the **TV code** matches, and press **Approve**. The TV signs in by itself. A code that runs out is replaced: **A new code. The last one ran out.**
4. Or choose **Sign in with a password** and type your Maktaba username and password with the remote. A home that cannot sign a TV in with a code yet asks for the password.
5. Move with the arrows; the item in focus has a gold ring. Press the centre button to open it, and **Back** to go back.

### Permissions {#permissions}

The TV app asks Android for one permission, and only on Android 17 and later. On Android 16 and earlier it needs none, and **Before you start** never shows.

The first time you open the app, **Before you start** lists the permission with why the app needs it, whether it is **Required** or **Optional**, and where it stands. The button to press already has the focus: press the centre button. It comes back every time you return to the app while a required permission is missing. **Not now** (or **Back**) puts it away until the next time you return.

#### Local network (Nearby devices) {#permission-local-network}

- **What it is:** Android 17 asks before an app reaches devices on your local network. Android's dialog asks whether to allow Maktaba "to find, connect to, and determine the relative position of nearby devices", and Settings lists it as **Nearby devices**. Maktaba only uses it to reach your home.
- **Why:** your home is on your local network. The TV reaches it there to list your libraries and play.
- **Required.**
- **How to allow it:** with **Allow** in focus, press the centre button, then choose **Allow** in Android's dialog.
- **Without it:** the TV cannot reach a home's local address. Only a home's internet address works.
- **If you refused it:** Android asks twice at most. After the second **Don't allow**, the row says **Not allowed, and Android will not ask again** and the button is **Open settings**. It opens Maktaba's page in the TV's settings: choose **Permissions**, then **Nearby devices**, then **Allow**. (To get there by hand: Settings → Apps, sometimes under **See all apps**, → Maktaba → Permissions.) Press **Back** to return to Maktaba; it checks again by itself.

The TV app asks for nothing else. It posts no notifications, plays nothing in the background and does not use the microphone or your location.

## What the states and messages mean {#states}

In **Before you start**:

- **Allowed**: the app has the permission. **Continue** has the focus.
- **Not asked yet**: the app has not asked. Press **Allow**.
- **Not allowed**: you refused it; Android can still ask. Press **Allow**.
- **Not allowed, and Android will not ask again**: only Settings can turn it on. Press **Open settings**.
- **Not needed on this Android version**: this Android has no such permission.
- **Required** / **Optional**: whether the app works without it. While a required one is missing, **Before you start** comes back each time you return to the app.
- **Without it, only a home's internet address works.**: the permission was refused.

## FAQ {#faq}

### Why does a TV app ask about nearby devices? {#faq-nearby-devices}

On Android 17, reaching a device on your local network falls under the **Nearby devices** permission. Your home is such a device. Maktaba does not look for, or connect to, anything else.

### Can I use the TV app without the permission? {#faq-without-permission}

Yes, with your home's internet address. **Before you start** comes back each time you return to the app until you allow it.

## When something goes wrong {#troubleshooting}

### The TV cannot reach my home {#cannot-reach-home}

- Check that the TV and the home are on the same network.
- On Android 17 and later, check that **Local network** is **Allowed** (see [Permissions](/help/android-tv#permissions)).
- Check the address: the home's address and port, like `192.168.1.20:8088`.
- If your home is not listed in **Choose your home**, the TV and the home may be on different networks, or Local network may be refused: choose **Enter an address**.

### Allow does nothing {#allow-does-nothing}

Android stops showing its dialog after you refuse a permission twice. The row then says **Not allowed, and Android will not ask again**: press **Open settings**, then choose **Permissions**, **Nearby devices**, **Allow**.

### I allowed it in Settings but the app still says Not allowed {#still-not-allowed}

Press **Back** until you are in Maktaba again: it checks when it comes back. If it still says **Not allowed**, open Settings → Apps → Maktaba → Permissions and check that **Nearby devices** is allowed.
