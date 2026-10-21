# The Mac app

Maktaba.app runs your whole library on a Mac: the library, transcription, search, Live TV and the web app, in one installation that starts when you log in.

## What it is {#what-it-is}

Maktaba.app is a Mac app with two jobs:

- **Run Maktaba on this Mac.** The Mac becomes your Maktaba server. It keeps the library, transcribes and streams. The server starts when you log in and keeps running when the window is closed. A menu-bar item shows whether it is running.
- **Connect to another Maktaba.** The app is a window onto a Maktaba that runs elsewhere. Nothing runs on this Mac.

The window shows the same Maktaba you see in a browser.

## How to use it {#how-to-use}

### Install {#install}

1. Open the downloaded disk image and drag **Maktaba** to **Applications**.
2. Open Maktaba from Applications. It needs macOS 13 or later.
3. If macOS says it cannot check the app, Control-click it in Applications, choose **Open**, then **Open** again.

### First-run setup {#first-run-setup}

The first time you open the app, it shows **Welcome to Maktaba**. Choose **Run Maktaba on this Mac** (**Set up this Mac**) or **Connect to another Maktaba** (type **Its address** and click **Connect**).

Setting up this Mac takes these steps (**Step N of N**, with **Back** and **Next**):

1. **What will run**: the app checks this Mac and lists what will run, the **Memory** and **Free disk space** (with what is needed) and the **Data folder**.
2. **Your admin account**: a **Username** and a **Password**, twice. You sign in to Maktaba with it, so keep the password somewhere safe.
3. **Your library folders**: the folders that hold your lectures, one per line. Maktaba reads them; it never moves or changes your files. If other machines see the same files under another path, fill in **NAS path map (optional)**, one `their path => this Mac's path` per line.
4. **Transcription models**: choose **Download from Hugging Face** (paste a **Hugging Face token**; **Get a token** shows where to make one) or **Use the copy on the NAS** (give the **Folder with the models**). The models are a few GB.
5. **Plex (optional)**: **Sign in with Plex**, type the code shown on the Plex page, and wait. Or click **Skip**; you can do it later in [Settings](/settings).
6. **Getting Maktaba ready**: the app installs the transcription and search tools (about 2 GB, a few minutes) and creates the library database. If macOS asks, allow Maktaba to run in the background (see [Allow Maktaba to run in the background](/help/mac-app#background-item)).
7. **Starting Maktaba**: the server starts and your library opens.

### Closing the window {#closing-the-window}

Closing the window only hides it. The server keeps running, and people on your network can keep watching. Open the window again with **Open Maktaba** in the menu bar.

### The menu bar {#menu-bar}

The Maktaba item in the menu bar shows the server's state on its first line and offers:

- **Update ready: restart to install** (only when an update is waiting).
- **Open Maktaba**: shows the window.
- **Pause server** / **Resume server**: stops the server or starts it again.
- **Open logs**: opens the folder with the server's log files in Finder.
- **Play** and **Pause playback**: control the video that is playing.
- **Settings**: opens Maktaba's settings in the window.
- **Quit (server keeps running)**: closes the app; the server keeps serving.
- **Stop server and quit**: stops the server and closes the app.

When the app is connected to another Maktaba, the menu has **Connect to another Maktaba…** instead of the server items, and a plain **Quit**.

### Starting at login {#login-item}

When setup finishes, the server is added to your Mac's login items. It appears as **Maktaba** in System Settings → General → Login Items, and starts every time you log in, with or without the window. If a part of it crashes, it is started again by itself.

**Pause server** stops it until you choose **Resume server** or log in again.

### Updates {#updates}

The app checks for a new version when it starts and every 6 hours. When one is ready, the menu bar shows **Update ready: restart to install**. Click it: the app downloads the update, restarts the server on the new version and opens again. A paused server stays paused and runs the new version when you resume it.

## What the states and messages mean {#states}

- **Maktaba is running**: every part of the server is up.
- **Maktaba is starting…**: the server has just started, or some parts are still coming up or restarting.
- **Maktaba: … keeps stopping**: the named parts keep failing. See [Some parts keep stopping](/help/mac-app#keeps-stopping).
- **Maktaba is paused**: you paused the server from the menu.
- **Maktaba is stopped**: the server is not answering and was not just started.
- **Maktaba: finish setup to start the server**: setup has not finished, so there is no server yet.
- **The server is starting…** / **Opening your library…** (in the window): wait; the library opens by itself.
- **Maktaba runs in the menu bar. Closing this window leaves it running.**: a reminder that closing the window does not stop the server.

## FAQ {#faq}

### Does Maktaba keep running when I close the window? {#faq-close}

Yes. Only **Stop server and quit** or **Pause server** stop the server.

### Where are the server's logs? {#faq-logs}

Choose **Open logs** in the menu bar. It opens the folder with the server's log files. Inside Maktaba, administrators can also read them on [Logs](/help/logs).

### Where are my lecture files kept? {#faq-files}

Where they already are. Maktaba reads the folders you chose and never moves or changes them.

### How do I switch from another Maktaba to running one on this Mac? {#faq-switch}

Choose **Connect to another Maktaba…** in the menu bar. It takes you back to the setup screens, where you can choose **Run Maktaba on this Mac**.

## When something goes wrong {#troubleshooting}

### Hugging Face did not accept the token {#hf-token-invalid}

- **What it means:** the Hugging Face token you pasted is wrong or was deleted.
- **Does Maktaba fix it itself?** No. The step waits for you.
- **What you can do:** make a new read token on huggingface.co (**Get a token**), paste it and try again.

### Accept the model's terms on huggingface.co {#hf-gated}

- **What it means:** the second-listen model (Cohere) needs you to accept its terms before it can be downloaded.
- **Does Maktaba fix it itself?** No.
- **What you can do:** sign in to huggingface.co with the same account, open the model's page, accept its terms, then try again.

### That folder has no models {#nas-missing}

- **What it means:** the folder you gave for **Use the copy on the NAS** does not hold the models.
- **Does Maktaba fix it itself?** No.
- **What you can do:** check that the NAS is mounted and pick the folder that holds the models, or choose **Download from Hugging Face**.

### The disk is full {#disk-full}

- **What it means:** there is no room for the models or the tools.
- **Does Maktaba fix it itself?** No.
- **What you can do:** free some space on this Mac (the **What will run** screen shows how much is needed), then try again.

### The Plex code expired {#pin-expired}

- **What it means:** the code was not entered on the Plex page in time.
- **Does Maktaba fix it itself?** No.
- **What you can do:** click **Sign in with Plex** again and enter the new code, or **Skip** and connect Plex later.

### Installing the tools failed {#venv-failed}

- **What it means:** downloading or installing the transcription and search tools stopped, often because the internet connection dropped.
- **Does Maktaba fix it itself?** No, but **Try again** carries on.
- **What you can do:** check your internet connection and click **Try again**. **Details** shows what failed.

### This copy of Maktaba cannot run the server {#server-missing}

- **What it means:** this build of the app does not include the server.
- **Does Maktaba fix it itself?** No.
- **What you can do:** download the full Maktaba.app, or choose **Connect to another Maktaba**.

### This Mac cannot run the Maktaba server {#mac-blocked}

- **What it means:** the check found this Mac lacks something the server needs; the **Memory** and **Free disk space** lines show what.
- **Does Maktaba fix it itself?** No.
- **What you can do:** free disk space if that is the problem, or click **Back** and connect to a Maktaba that runs on another machine.

### Allow Maktaba to run in the background {#background-item}

- **What it means:** macOS turned off Maktaba's background item, so the server cannot start at login.
- **Does Maktaba fix it itself?** No. macOS needs your permission.
- **What you can do:** click **Open Login Items**, turn on **Maktaba**, come back and click **I turned it on**.

### The server is stopped {#server-stopped}

- **What it means:** the server is not running, because you paused or stopped it.
- **Does Maktaba fix it itself?** No. It starts again at your next login.
- **What you can do:** choose **Resume server** in the menu bar.

### Some parts keep stopping {#keeps-stopping}

- **What it means:** parts of the server (for example the pipeline or streaming) keep failing.
- **Does Maktaba fix it itself?** It keeps restarting them, more slowly each time. The other parts keep running.
- **What you can do:** choose **Open logs** in the menu bar to see why. A full disk is a common cause. See also [System health](/help/system-health#crash-loop).

### The update could not be installed {#update-failed}

- **What it means:** downloading or installing the new version failed. The message gives the reason.
- **Does Maktaba fix it itself?** It checks again later and shows **Update ready** again.
- **What you can do:** check your internet connection and click **Update ready: restart to install** again.
