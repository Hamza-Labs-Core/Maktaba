# The Android apps

Maktaba Player and Maktaba Remote bring your home to Android phones, foldables and tablets.

## What it is {#what-it-is}

There are two apps:

- **Maktaba Player**: browse your home's libraries and watch.
- **Maktaba Remote**: run your home from your phone. It opens on **Needs attention** (with **Retry** and **Retry all**), then **Queue** (pause and resume a stage or everything), **Libraries** (scan) and **Machines** (drain). Only the home's admins can use it; the home refuses everyone else.

Each app is one app for phones, foldables and tablets, and needs Android 8 or later. The screen follows the window, not the device:

- **One pane** on a phone, or a foldable when it is folded.
- **The list beside the page** when the window is 600 dp or wider (a tablet, a foldable open flat).
- **Split at the hinge** when a foldable is half folded. Held like a laptop (tabletop), the video is on the top half and the list on the bottom. Held like a book, they sit side by side.

Both apps are in English and Arabic. Android's per-app language setting (Settings → Apps → the app → Language) lists both.

## How to use it {#how-to-use}

### Choose a home and sign in {#choose-home}

1. Open the app. On Android 17 and later, the first time, it shows **Before you start** (see [Permissions](/help/android-apps#permissions)).
2. Choose your home. **On this network** lists the homes that announce themselves on your network. **Your homes** lists the homes you used before. If yours is not listed, tap **Enter an address** and type its address, like `192.168.1.20:8088`, then **Connect**.
3. Sign in with your Maktaba **Username** and **Password**. The app stays signed in to that home until you sign out.

To use another home, tap **Choose another home** on the sign-in screen.

### Through Maktaba Cloud {#through-cloud}

Away from home, or for a home on another network, open it through Maktaba Cloud. Your account on the home must be linked to your Maktaba Cloud account first, from the home's web page (see [Linked accounts](/help/account#linked-accounts)).

1. In the home picker, tap **Through Maktaba Cloud**.
2. Sign in with your Maktaba Cloud **Email** and **Password**. (**Advanced** sets another **Maktaba Cloud address**, for a test or your own Maktaba Cloud; leave it empty for the usual one.)
3. **Your homes** lists the homes linked to your cloud account, **Online** or **Offline**. A home marked **Not linked to your account** cannot be opened: link it on that home first. Owning a home in Maktaba Cloud is not an account in it.
4. Tap a home. The app reaches it at its own address when you are on its network, otherwise through Maktaba Cloud, and signs you in there without your password.

The home decides what you can do: you are your own account there, and through the cloud only watching, unless you are an admin of the home who allowed managing for your own link. The app keeps both of the home's addresses and uses whichever answers. A sign-in belongs to the address it was made at, so at the other address the app signs in again, through the cloud when the cloud gave that address and you are signed in to it, otherwise with your password or a code. The cloud sign-in lasts until the app quits; **Sign out of Maktaba Cloud** ends it sooner. Signing out of the cloud does not sign you out of your homes.

### Permissions {#permissions}

The apps ask Android for one permission, and only on Android 17 and later. On Android 16 and earlier they need none, and **Before you start** never shows.

The first time you open an app, **Before you start** lists the permission with why the app needs it, whether it is **Required** or **Optional**, and where it stands. It comes back every time you return to the app while a required permission is missing. **Not now** puts it away until the next time you return. When a home is chosen, **Learn more** opens this page on your home.

#### Local network (Nearby devices) {#permission-local-network}

- **What it is:** Android 17 asks before an app reaches devices on your local network. Android's dialog asks whether to allow the app "to find, connect to, and determine the relative position of nearby devices", and Settings lists it as **Nearby devices**. Maktaba only uses it to reach your home; it does not use your location.
- **Why:** your home is on your local network. The apps find it there (it announces itself) and talk to it at its local address.
- **Required.**
- **How to allow it:** tap **Allow** under **Local network**, then **Allow** in Android's dialog. The home picker has the same **Allow** button under **Find homes on this network**.
- **Without it:** the app cannot find homes on your network or reach a home's local address. Only a home's internet address works.
- **If you refused it:** Android asks twice at most. After the first **Don't allow**, the row says **Not allowed** and **Allow** asks again. After the second, it says **Not allowed, and Android will not ask again** and the button is **Open settings**. It opens the app's page in Settings: tap **Permissions**, then **Nearby devices**, then **Allow**. Go back to the app; it checks again by itself.

The apps ask for nothing else. They post no notifications, play nothing in the background (a video pauses when you leave the app), and do not use your location, microphone or contacts.

## What the states and messages mean {#states}

In **Before you start**:

- **Allowed**: the app has the permission.
- **Not asked yet**: the app has not asked. Tap **Allow**.
- **Not allowed**: you refused it; Android can still ask. Tap **Allow**.
- **Not allowed, and Android will not ask again**: only Settings can turn it on. Tap **Open settings**.
- **Not needed on this Android version**: this Android has no such permission.
- **Required** / **Optional**: whether the app works without it. While a required one is missing, **Before you start** comes back each time you return to the app.

In the home picker:

- **Looking for homes…**: the app is listening for homes on your network.
- **No home has answered on this network yet.**: no home announced itself. Type its address.
- **Find homes on this network**: the local network permission is missing. Tap **Allow**, or **Open settings** when Android will not ask again.
- **Without it, only a home's internet address works.**: the permission was refused.

In Maktaba Remote, **For the home's admins** means the user who signed in is not the home's admin. They can watch with Maktaba Player.

## FAQ {#faq}

### Why does Maktaba ask about nearby devices? {#faq-nearby-devices}

On Android 17, reaching a device on your local network falls under the **Nearby devices** permission. Your home is such a device. Maktaba does not look for, or connect to, anything else.

### Can I use the apps without the permission? {#faq-without-permission}

Yes, with a home's internet address. Choose **Not now**, then **Enter an address**. **Before you start** comes back each time you return to the app until you allow it.

### Why don't the apps ask to send notifications? {#faq-notifications}

They post none. Nothing plays or runs in the background, so they also need no battery exemption.

## When something goes wrong {#troubleshooting}

### No home appears on this network {#no-home-found}

- Check that the phone and the home are on the same Wi-Fi network.
- On Android 17 and later, check that **Local network** is **Allowed** (see [Permissions](/help/android-apps#permissions)).
- Some routers block announcements between devices. Tap **Enter an address** and type the home's address, like `192.168.1.20:8088`.

### Allow does nothing {#allow-does-nothing}

Android stops showing its dialog after you refuse a permission twice. The row then says **Not allowed, and Android will not ask again**: tap **Open settings**, then **Permissions**, **Nearby devices**, **Allow**.

### I allowed it in Settings but the app still says Not allowed {#still-not-allowed}

Go back to the app (do not open it again from the start). It checks when it comes back. If it still says **Not allowed**, open the app's page in Settings again and check that **Nearby devices** says **Allowed** for Maktaba Player or Maktaba Remote (each app has its own).
