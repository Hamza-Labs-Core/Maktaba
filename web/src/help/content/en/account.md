# Your account

Your profile and password, where you are signed in, your API tokens, your watch history and your privacy.

## What it is {#what-it-is}

Your account pages are listed in the side menu (on a phone, under [More](/more)): **Account**, **Devices**, **Sessions**, **API tokens** and **History**. The **Privacy** tab in Settings controls whether your viewing is recorded.

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
- To sign out of every device at once, open [Settings → Account](/settings) and press **Sign out everywhere**. You are signed out here too.

### Devices {#devices}

[Devices](/account/devices) lists the phones and apps registered to receive push notifications for you, with their app version and when they were last seen. Press **Remove** to stop notifications to one of them.

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

### Privacy: pause tracking {#privacy}

On [Settings → Privacy](/settings/privacy), the **Activity tracking** switch decides whether your plays are recorded in watch analytics.

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

## FAQ {#faq}

### I lost my phone. What do I do? {#faq-lost-device}

Open [Sessions](/account/sessions) and press **Revoke** on that phone's session, or use **Sign out everywhere** in [Settings → Account](/settings). Then change your password.

### I lost a token I created. Can I see it again? {#faq-lost-token}

No. A token is shown once. Revoke the old one and create a new one.

### Does pausing tracking delete my history? {#faq-pause-deletes}

No. It only stops new plays from being recorded. Remove old entries one by one on the [History](/account/history) page.

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

### Something went wrong. (a page does not load) {#load-error}

- **What it means:** The page could not read your data from the server.
- **Does Maktaba fix it itself?** No, the page waits for you.
- **What you can do:** Press **Try again**. If it keeps failing, ask an admin.
