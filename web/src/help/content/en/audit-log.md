# Audit log

See who signed in, who changed what and when. Only administrators see this page.

## What it is {#what-it-is}

[Administration → Audit log](/admin/audit) is a record Maktaba keeps of important events, newest first. You cannot edit or delete it from here. It has two sources:

- **Security events**: sign-ins (successful and failed), sign-outs, locked accounts, password changes, sessions and app sign-ins ended, pairing codes for TV and apps, refused permissions and other security events.
- **Library**: changes to one library, such as its settings, deletions and merged speakers.

Each row shows the **Timestamp**, the **Event**, the **Actor** (the ID of the user who did it), the **Target** (what it was done to) and **Details**.

## How to use it {#how-to-use}

### Look at security events {#security-events}

1. Open [Administration → Audit log](/admin/audit). **Source** starts on **Security events**.
2. The newest 50 rows load. Click **Load more** at the bottom for older ones.
3. Click **Refresh** to fetch the latest.

### Look at one library's history {#library-events}

1. Set **Source** to **Library**.
2. Type the **Library ID**. It is the last part of the library's address, after `/library/`, when you open it from the [Library](/library).

### Filter by user or event {#filter}

- **Event**: pick one kind of event, or **All events**.
- **Actor**: type a user's ID (or part of it) to keep only what that user did. The user's ID is shown on the [Users](/admin/users) page.
- **From** and **To**: keep only events in that time window.

The filters work on the rows already loaded. If you are looking for something older, click **Load more** until it appears.

### Export {#export}

Click **Export CSV** to save the rows that match your filters. The file opens in a spreadsheet with Arabic text intact.

## What the states and messages mean {#states}

- **login.success** / **login.failed**: a sign-in worked or failed.
- **lockout-username** / **lockout-ip**: sign-ins were refused after too many failed tries.
- **password.changed**: a password was changed.
- **session.revoked** / **refresh.revoked**: an administrator ended a sign-in.
- **refresh.replay-detected**: an old app sign-in was reused; Maktaba ended it to be safe.
- **permission.denied**: someone tried something they are not allowed to do.
- **pair.code-issued** / **pair.code-claimed**: a pairing code for a TV or app was made and used.
- **Enter a library ID to load its audit feed.**: the Library source needs an ID first.
- **No audit events**: nothing matches the source and filters.

## FAQ {#faq}

### Why does the Actor column show an ID and not a name? {#faq-actor-id}

The log stores the user's ID so that it stays correct even if the user is renamed. Match it with the **ID** column on [Users](/help/users).

### Why does my filter miss an old event? {#faq-filter-old}

Filters only search the rows already loaded. Click **Load more** to bring in older rows, then filter again.

### Does the audit log record every change in Maktaba? {#faq-everything}

No. It records security events and library changes. Processing activity is on [Processing](/help/processing), and what the server does in detail is in [Logs](/help/logs).

## When something goes wrong {#troubleshooting}

### Something went wrong (with Try again) {#load-failed}

- **What it means:** the events could not be loaded, for example the library ID does not exist.
- **Does Maktaba fix it itself?** No. The page waits for you.
- **What you can do:** check the **Library ID**, then click **Try again**.

### Load more fails {#load-more-failed}

- **What it means:** the next page of older events could not be loaded. The rows already shown stay.
- **Does Maktaba fix it itself?** No.
- **What you can do:** click **Try again** at the bottom of the list.

### Many failed sign-ins {#many-failed-sign-ins}

- **What it means:** someone keeps typing wrong passwords, a person who forgot theirs or someone guessing.
- **Does Maktaba fix it itself?** Yes. After 5 wrong tries the account is locked for 15 minutes.
- **What you can do:** if it is a real user, reset their password on [Users](/help/users#reset-a-password). If not, do not expose Maktaba's address to people you do not know.
