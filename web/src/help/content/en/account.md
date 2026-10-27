# Your account

Your profile and password, where you are signed in, your API tokens, your watch history and your privacy.

## What it is {#what-it-is}

Your account pages are listed in the side menu (on a phone, under [More](/more)): **Account**, **Devices**, **Connect an app**, **Sessions**, **API tokens**, **History** and **Preferences**. [Preferences](/account/preferences) holds your own choices: how Maktaba looks, your privacy, and signing out everywhere. Every app has it, the player apps included.

## How to use it {#how-to-use}

### Profile {#profile}

On [Account](/account), the **Profile** card shows your **Username** (it cannot be changed) and lets you change your **Display name** and **Email**. Press **Save**; you see **Profile updated.**

### Change your password {#change-password}

On [Account](/account), in **Change password**:

1. Type your **Current password**.
2. Type a **New password** of at least 8 characters, and type it again in **Confirm password**.
3. Press **Change password**. You see **Password changed.**

### Sessions: where you are signed in {#sessions}

[Sessions](/account/sessions) lists every browser and device signed in to your account, with its IP address, when it signed in and when it was last active. Your own is marked **This session**.

- Press **Revoke** on a session you do not recognise, or on a lost device. That device is signed out.
- To sign out of every device at once, open [Preferences → Account](/account/preferences/account) and press **Sign out everywhere**. You are signed out here too.

### Devices {#devices}

[Devices](/account/devices) lists the phones and apps registered to receive push notifications for you, with their app version and when they were last seen. Press **Remove** to stop notifications to one of them.

To sign in a phone, tablet, computer or TV with a code instead of your password, see [Sign in a device with a code](#sign-in-with-a-code).

### Connect an app {#connect-app}

[Connect an app](/account/connect) helps a Maktaba app on your phone, tablet, computer or TV find this home:

1. Open the app. On a device on the same network, the app may already list this home: pick it.
2. Otherwise, choose to add a home in the app and scan the QR code on this page with it.
3. Or type one of the **Addresses** into the app. Press **Copy** next to an address to copy it, or **Copy the app link** to copy the link the QR code holds.

The addresses on this network come first. When this home is connected to Maktaba Cloud, its address through the cloud comes last; that one works from anywhere. Then sign in on the app with your username and password, or with a code.

### Sign in a device with a code {#sign-in-with-a-code}

An app can sign in without your password: it shows a code of eight letters and digits, and a QR code.

1. Scan the app's QR code with your phone's camera, or open [Sign in a device](/pair) here and type the code. Press **Continue**.
2. The page shows what is asking: the app, the device and its name, and when the code expires. Check that it is the device in front of you.
3. Press **Approve**. The device is signed in as you, with your own access and nothing more. Press **Cancel** if you do not recognise it.

A code can be approved once, within 10 minutes.

### Linked accounts: Maktaba Cloud {#linked-accounts}

When this home is connected to Maktaba Cloud, you can link your Maktaba Cloud account to your account here. Then the apps, and Maktaba Cloud's website, can sign you in to this home through the cloud, from anywhere, without your password.

1. On [Account](/account), under **Linked accounts**, press **Link a Maktaba Cloud account**.
2. Maktaba Cloud opens. Sign in to it if it asks, check the home's address, and press **Link**. (**Cancel** comes back without linking.)
3. You come back here and see **Your Maktaba Cloud account is linked.** The card shows the cloud account and what it may do here.

What a linked account may do:

- **Watch**: the default. Through the cloud you are this account, with its libraries, and managing the home is refused, even for an admin. A stolen cloud account can watch, not manage.
- **Watch and manage**: only an admin can choose it, for their own link, from a sign-in on the home itself: press **Allow managing through the cloud**. **Watch only through the cloud** goes back.

The link gives the cloud account no rights by itself: the home always checks your own role. One cloud account can be linked to one person on each home. **Unlink** ends every sign-in through the cloud at once; signing in on the home itself is not affected. A home's admin can also unlink anyone (see [Users](/help/users#revoke-access)).

When you signed in through Maktaba Cloud, you cannot link, change the link or approve a device from that sign-in; sign in on the home itself.

### API tokens {#api-tokens}

A token lets a script or the command line use Maktaba as you, without your password.

1. On [API tokens](/account/tokens), press **Create token**.
2. Give it a **Name** you will recognise.
3. Under **Scopes**, tick **Read**, **Write** or **Admin**, or leave all unticked to give it your full permissions.
4. Pick an **Expiry**: **Never**, 30, 90 or 365 days.
5. Press **Create token**. **Copy your new token** shows the token. Press **Copy** and keep it somewhere safe: this is the only time it is shown.

Later, the list shows only the token's **Prefix**, its **Scopes**, when it was **Last used** and when it **Expires**. Press **Revoke** to stop a token for good; it stays in the list marked **Revoked**.

### Watch history {#watch-history}

[History](/account/history) has two tabs:

- **Watched**: every video you watched, newest first, with your progress, how many times, and when. **Remove** takes a video out of your history and also forgets where you stopped in it.
- **Activity timeline**: what you watched and searched, day by day. Use **Show** to pick **Everything**, **Watched** or **Searched**.

### Preferences {#preferences}

[Preferences](/account/preferences) has three tabs, the same in every edition of Maktaba (the home's web page, the player apps and the remote):

- **Appearance**: the **Theme** (light, dark or the system's), the **Language** (English or العربية) and the **Density** (comfortable or compact). These are kept in this browser or app, so set them on each device.
- **Privacy**: whether your plays are recorded, whether this app sends anonymous usage data, and your recent history and activity (see [Privacy](#privacy)).
- **Account**: **Sign out everywhere**, a link to your [API tokens](/account/tokens), and **Diagnostics**, a support bundle of your own activity.

On the home's web page, admins also find these tabs in [Settings](/settings), beside the home's own settings. Changing them in one place changes them in the other.

### Privacy: pause tracking {#privacy}

On [Preferences → Privacy](/account/preferences/privacy), the **Activity tracking** switch decides whether your plays are recorded in watch analytics.

- **Tracking on**: your plays count in the statistics and in your history.
- **Tracking paused**: new plays are not recorded. Searches and your existing history are not affected.

The same tab shows your recent **Watch history**, with **Remove** on each row, and **Recent activity**. **See full watch history** and **See the whole timeline** open the full pages.

## What the states and messages mean {#states}

- **This session** / **Current**: the session you are using now. It cannot be revoked from the list; use **Sign out** instead.
- **Unknown device**: the browser did not say what it is.
- **Session revoked.**: that device is signed out.
- **No active sessions**: no other device is signed in.
- **No devices**: no phone or app is registered for push notifications.
- **Full access**: the token has all of your permissions.
- **Revoked**: the token no longer works.
- **Copied!** / **Couldn't copy to clipboard.**: the copy worked, or it did not; select the token and copy it by hand.
- **Removed from your history.**: the video is gone from your history.
- **Nothing watched yet** / **No activity yet.**: there is nothing to show yet.
- **Privacy settings saved.**: the tracking switch was changed.
- **The device is signed in. You can close this page.**: you approved the code; the device is signed in as you.
- **This code was already used.**: someone approved this code already. Ask the device for a new code.
- **This code is not waiting for a sign-in**: the code expired, was already used, or the home restarted since the device asked. Ask the device for a new code.
- **Check the code**: what you typed is not eight letters and digits.
- **That Maktaba Cloud account is already linked to another user of this home.**: unlink it from that account first, or link another cloud account.
- **The link step ran out or was already used. Start again.**: press **Link a Maktaba Cloud account** again.
- **This home is not connected to Maktaba Cloud, so it has no linked accounts.**: an admin connects it to Maktaba Cloud first (see [Devices and cloud](/help/cloud)).
- **Copied.** / **Couldn't copy.**: on Connect an app, the copy worked, or it did not; select the text and copy it by hand.
- **This home has no address another device can use yet.**: the home does not know its address on the network. Type the address you use to open Maktaba in this browser into the app instead.

## FAQ {#faq}

### I lost my phone. What do I do? {#faq-lost-device}

Open [Sessions](/account/sessions) and press **Revoke** on that phone's session, or use **Sign out everywhere** in [Preferences → Account](/account/preferences/account). Then change your password.

### I lost a token I created. Can I see it again? {#faq-lost-token}

No. A token is shown once. Revoke the old one and create a new one.

### Does pausing tracking delete my history? {#faq-pause-deletes}

No. It only stops new plays from being recorded. Remove old entries one by one on the [History](/account/history) page.

### Can a device I approve do more than I can? {#faq-approve-rights}

No. A device signed in with a code is signed in as you, with your own access. To stop it, revoke its session on [Sessions](/account/sessions).

### Can I delete my account? {#faq-delete-account}

Not by yourself. Ask an admin.

## When something goes wrong {#troubleshooting}

### current password is incorrect {#wrong-current-password}

- **What it means:** The **Current password** you typed is not your password.
- **Does Maktaba fix it itself?** No.
- **What you can do:** Type it again. If you forgot it, sign out and ask an admin to reset it.

### new password must differ from the current one {#same-password}

- **What it means:** The new password is the same as the current one.
- **Does Maktaba fix it itself?** No.
- **What you can do:** Choose a different password.

### A script stopped working with my token {#token-stopped}

- **What it means:** The token was revoked, it expired, or its scopes do not allow what the script does.
- **Does Maktaba fix it itself?** No.
- **What you can do:** Check the token's **Expires** and **Scopes** on [API tokens](/account/tokens). Create a new token if needed and put it in the script.

### a sign-in through Maktaba Cloud cannot sign in a device {#cloud-cannot-approve}

- **What it means:** You opened Maktaba through Maktaba Cloud. A device signed in with a code keeps working after the cloud link ends, so only a sign-in on the home itself can approve one.
- **Does Maktaba fix it itself?** No.
- **What you can do:** Open the home's own address (on the same network as the home) and approve the code there.

### Something went wrong. (a page does not load) {#load-error}

- **What it means:** The page could not read your data from the server.
- **Does Maktaba fix it itself?** No, the page waits for you.
- **What you can do:** Press **Try again**. If it keeps failing, ask an admin.
