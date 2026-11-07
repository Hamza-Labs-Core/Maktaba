# The apps' privacy answers (App Store and Google Play)

What Maktaba's apps answer in App Store Connect's **App Privacy** and Google Play's **Data
safety** forms, kept with the code that makes them true (Epic 53 story 53.14 §3). A change to
what an app collects or sends changes this file in the same pull request.

## What the apps send, and where

- **To the home** (the person's own server, run by its admin; not Maktaba):
  - **Crash reports**, only when the person turned on **Send crash reports to {home}**. The
    switch is off on a fresh install and on an update from a release before it. The app asks
    once after signing in to a home, with two equal buttons, **Send crash reports** and
    **Not now**; afterwards it is in the app's settings (Profile on the Player, the Home tab
    on the Remote, Settings on the TV apps). A report holds the crash (its exception, the
    crashed thread's frames as binary and offset, the binaries' UUIDs), the app's version,
    the platform and system version, the device model, a random install id, and the app's own
    log lines, scrubbed with the shared redaction rules (`shared/redact/rules.json`: tokens,
    emails, home folders, card numbers and IP addresses are masked). It holds no videos,
    messages or file contents. The home refuses a report whose envelope does not carry the
    person's consent.
  - **Problem reports**, only when the person sends one from **Report a problem**. The form
    shows everything it will send first: the app and device, the app's log for the last 30
    minutes (readable in the preview, already scrubbed), the last request ids, the kept
    crashes (a tick box) and a screenshot only when the person ticks it and sees it.
- **To Maktaba**: nothing from the apps themselves. Anything that leaves the home is the home
  admin's choice and opt-in (Epic 53 story 53.16), never the app's.
- **No third party** receives crash or diagnostic data: no analytics or crash-reporting
  service is built in. The Apple TV app uses PLCrashReporter (MIT), a library that runs on the
  TV and sends nothing itself.

When the switch is off, nothing leaves the device: crashes are still kept on it (the Apple
apps keep the last 5; the Android apps keep `last-crash.txt` and, from Android 11, the last
5 ANRs, native crashes and low-memory kills with an ANR's trace, under `files/crashes/`), so
a person who later sends a problem report can choose to attach them, seeing them first.

Which apps do this today: the Apple apps (Player for iPhone, iPad and Mac, Remote, Apple TV),
the Android apps (Player, Remote, Android TV), the desktop apps and the web app. The desktop
Player (Windows, Linux) keeps the last 5 crashes in its settings folder; Maktaba's desktop
app keeps its own panics under its logs folder's `crashes/` and sends them through the web app
in its window, only when the switch there is on. An Android crash report holds the
exception's class and its frames, never its message (it can name a path or a person).

## App Store Connect: App Privacy

| Data type | Collected | Linked to the user | Used for tracking | Purpose |
|---|---|---|---|---|
| Diagnostics → **Crash Data** | Yes, optional (off until the person turns it on) | No | No | App Functionality |
| Diagnostics → **Other Diagnostic Data** | Yes, optional (a problem report the person sends) | No | No | App Functionality |
| Everything else | No | | | |

"Collected" here means sent off the device to a server the person chose (their home), as the
form defines it. The data is not linked to an identity at Maktaba, which never receives it.

## Google Play: Data safety

| Data type | Collected | Shared | Optional | Purpose |
|---|---|---|---|---|
| App info and performance → **Crash logs** | Yes | No | Yes (off until the person opts in) | App functionality |
| App info and performance → **Diagnostics** | Yes | No | Yes (only in a report the person sends) | App functionality |

Data is encrypted in transit when the home is reached over HTTPS (the cloud relay, or a home
with its own certificate); on a home's local network address it travels as the home serves
it. The person can turn crash reports off at any time; **My reports** shows what they sent and
the admin's reply.
