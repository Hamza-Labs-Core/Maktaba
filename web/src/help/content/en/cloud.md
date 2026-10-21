# Devices and cloud

Three pages about your installation itself: the devices that get notifications, your plan and licence, and the connection to Maktaba Cloud.

## What it is {#what-it-is}

- **[All devices](/admin/devices)** (under Administration): the phones, tablets and browsers registered to receive push notifications. Despite its name, it lists the devices registered to **your** account.
- **[Billing & license](/billing)**: your plan (**Tier**), how many accounts it allows (**Seats**), which features are on (**Entitlements**) and, for administrators, the licence key.
- **[Cloud status](/cloud/status)** (**Cloud server status**): whether this server is connected to a Maktaba Cloud relay.

Billing & license and Cloud status are in the side menu for every signed-in user. All devices is in the Administration menu.

## How to use it {#how-to-use}

### All devices {#devices}

The table shows each device's **Platform**, **App**, **App version**, **Last seen** and **Registered** date.

- **Edit** a device to change its **Locale** or its **Notification categories**: **Jobs**, **Library**, **Subscription** and **System**. Untick a category to stop that kind of notification on that device.
- **Remove** a device and confirm. It stops receiving notifications.
- **Register device** adds one by hand. You need the **Platform**, the app's **Push token** and its **Bundle ID**; this is mainly for testing.

### Billing & license {#billing}

**Current plan** shows your **Tier** (for example _free_), the **Seats** it allows and the **License ID**. **Entitlements** lists each feature as **Enabled** or **Disabled**.

To upgrade (administrators only):

1. Paste the signed licence document you received into **License key**.
2. Click **Apply license**. The message _License applied._ confirms it, and the plan and features update.

**Revoke license** removes the licence at once, without asking, and returns this server to the free tier (_Reverted to free tier._).

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

No. It is optional. To watch Live TV away from home, see [Remote TV](/help/remote-tv).

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
