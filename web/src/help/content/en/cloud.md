# Devices and cloud

Three pages about your installation itself: the devices that get notifications, your plan and licence, and the connection to Maktaba Cloud.

## What it is {#what-it-is}

- **[All devices](/admin/devices)** (under Administration): the phones, tablets and browsers registered to receive push notifications. Despite its name, it lists the devices registered to **your** account.
- **[Subscription](/settings/subscription)** (Settings → Subscription): this home's plan (Free, Home or Pro), its users against what the plan allows, what the plan includes and, for administrators, the license and Maktaba Cloud.
- **[Cloud status](/cloud/status)** (**Cloud server status**): whether this server is connected to a Maktaba Cloud relay.

Subscription and Cloud status are in the side menu for every signed-in user. All devices is in the Administration menu.

## How to use it {#how-to-use}

### All devices {#devices}

The table shows each device's **Platform**, **App**, **App version**, **Last seen** and **Registered** date.

- **Edit** a device to change its **Locale** or its **Notification categories**: **Jobs**, **Library**, **Subscription** and **System**. Untick a category to stop that kind of notification on that device.
- **Remove** a device and confirm. It stops receiving notifications.
- **Register device** adds one by hand. You need the **Platform**, the app's **Push token** and its **Bundle ID**; this is mainly for testing.

### Subscription {#billing}

**Plan** shows this home's plan and **Users**: how many people have an account, of how many the plan allows. **What the plan includes** lists remote access through Maktaba Cloud, more than one user, cloud backup, analytics beyond 30 days, and connecting other homes, each **Included** or **Not included**.

- **Free** is the whole library for one user on your home network.
- **Home** adds remote access, up to 4 users, cloud backup and the full analytics.
- **Pro** has no user limit and adds connecting other homes.

The home itself enforces the plan: when something is not included, the home refuses it and says why (for example _This home's Free plan allows 1 user and it has 1._). People who already have an account keep working even when the home has more users than its plan allows; only new ones are refused.

For administrators:

- **Maktaba Cloud**: a home linked to Maktaba Cloud gets the license of your cloud plan by itself and renews it every day. **Manage plan** (or **Get a plan**) opens your cloud account's billing page; **Check now** asks the license server at once.
- **License**: to use a license you received, paste it into **Paste a license** and click **Apply license**. It is checked, applied and never shown again. **Remove license** asks first, then returns the home to Free.
- A cloud license that cannot be renewed keeps working for about a month; then the home shows _Reconnect to validate the license_ and returns to Free until it can reach Maktaba Cloud again. A license Maktaba revokes stops at once.

### Cloud status {#cloud-status}

When this server is connected to a Maktaba Cloud relay, the page shows the **Tunnel** (**Connected** or **Disconnected**), the **Server ID** and the **Last heartbeat**. It refreshes every 30 seconds.

When it is not connected, the page says **Cloud relay not configured** and lists the steps under **Connect to Maktaba Cloud**: create a Maktaba Cloud account and register this server, set the relay endpoint and token in the server configuration, then restart the server.

## What the states and messages mean {#states}

- **Enabled** / **Disabled** (Entitlements): the feature is on or off in your plan.
- **License applied.**: the new licence is active.
- **Reverted to free tier.**: the licence was removed.
- **Connected**: the relay tunnel is up.
- **Disconnected**: the relay is set up but the tunnel is down.
- **Not reported**: the relay does not send that value (Bandwidth is never reported yet).
- **No devices**: no device of yours is registered for notifications.
- **Device registered.** / **Device updated.** / **Device removed.**: the change was saved.

## FAQ {#faq}

### Why does All devices show only my devices? {#faq-only-mine}

The server only lists the devices of the signed-in account. Each user manages their own.

### Is All devices the same as the devices I am signed in on? {#faq-signed-in}

No. This page is about notifications. To see where you are signed in, open **Sessions** in the side menu.

### Do I need a licence to use Maktaba? {#faq-licence}

No. Without a licence Maktaba runs on the free tier. The **Entitlements** table shows what that includes.

### Do I need Maktaba Cloud? {#faq-cloud}

No. It is optional. To watch Live TV away from home, see [Remote TV](/help/remote-tv). Once your home is connected, each person can link their own Maktaba Cloud account to sign in through it from anywhere: see [Linked accounts](/help/account#linked-accounts).

## When something goes wrong {#troubleshooting}

### The license document was rejected {#license-rejected}

- **What it means:** the pasted text is not a valid signed licence, or it was changed. The server's reason may follow.
- **Does Maktaba fix it itself?** No. Your current plan stays as it was.
- **What you can do:** paste the whole document again exactly as you received it, including the braces. If it still fails, ask for a new licence.

### License management is disabled in this build {#license-disabled}

- **What it means:** this copy of Maktaba was built without licence checking, so it cannot accept a licence.
- **Does Maktaba fix it itself?** No.
- **What you can do:** install an official release of Maktaba, then apply the licence.

### seat limit reached for the current subscription tier {#seat-limit}

- **What it means:** you tried to add a user but all the seats of your plan are used.
- **Does Maktaba fix it itself?** No.
- **What you can do:** remove an account on [Users](/help/users) or apply a licence with more seats.

### Could not reach the server health endpoint {#cloud-unreachable}

- **What it means:** Cloud status could not ask the server how it is; the **Tunnel** shows **Down**.
- **Does Maktaba fix it itself?** The page tries again every 30 seconds.
- **What you can do:** check [System health](/help/system-health). If the server itself is down, start it again.

### Disconnected {#relay-down}

- **What it means:** the cloud relay is set up but its tunnel is not connected.
- **Does Maktaba fix it itself?** The page checks again every 30 seconds and shows **Connected** once the tunnel is back.
- **What you can do:** check this server's internet connection and the relay token in the server configuration, then restart the server.
