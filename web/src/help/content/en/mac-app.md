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
2. Open Maktaba from Applications. It needs macOS 15 or later.
3. If macOS says it cannot check the app, Control-click it in Applications, choose **Open**, then **Open** again.

### First-run setup {#first-run-setup}

The first time you open the app, it shows **Welcome to Maktaba**. Choose **Run Maktaba on this Mac** (**Set up this Mac**) or **Connect to another Maktaba** (type **Its address** and click **Connect**).

Setting up this Mac takes these steps (**Step N of N**, with **Back** and **Next**):

1. **What will run**: the app checks this Mac and lists what will run, the **Memory** and **Free disk space** (with what is needed) and the **Data folder**.
2. **Where Maktaba keeps its data**: the disks of this Mac with their free space. Maktaba picks a folder on the disk with the most room; choose another disk or type a folder. A disk too small cannot be chosen, and one that is small for a library database plus backups is flagged. You can move the data later from [Storage & discovery](/help/storage#move).
3. **Your admin account**: a **Username** and a **Password**, twice. You sign in to Maktaba with it, so keep the password somewhere safe.
4. **Your library folders**: the folders that hold your lectures, one per line. Maktaba reads them; it never moves or changes your files. If other machines see the same files under another path, fill in **NAS path map (optional)**, one `their path => this Mac's path` per line.
5. **Transcription models**: choose **Download from Hugging Face** (paste a **Hugging Face token**; **Get a token** shows where to make one) or **Use the copy on the NAS** (give the **Folder with the models**). The models are a few GB.
6. **Plex (optional)**: **Sign in with Plex**, type the code shown on the Plex page, and wait. Or click **Skip**; you can do it later in [Settings](/settings).
7. **Permissions**: what Maktaba needs from macOS, whether each one is on, and **Open System Settings** to turn it on. See [Permissions](#permissions). You can go on without them and turn them on later.
8. **Getting Maktaba ready**: the app installs the transcription and search tools (about 2 GB, a few minutes) and creates the library database. If macOS asks, allow Maktaba to run in the background (see [Allow Maktaba to run in the background](/help/mac-app#background-item)).
9. **Starting Maktaba**: the server starts and your library opens.

### Permissions {#permissions}

macOS keeps some things private until you allow them. Maktaba asks only for what the server needs. The setup's **Permissions** step lists them, and the window reminds you (**Maktaba needs a permission on this Mac**) whenever a required one is off. It checks again each time you come back to the window. Each item says why it is needed, whether it is on, and has **Open System Settings**, which opens the right page.

- **Full Disk Access** (required): lets the server read your library folders, a NAS share and the folders macOS protects. Without it, reading them fails with "operation not permitted" and nothing new is found or processed.
  1. Click **Open System Settings**. It opens Privacy & Security → Full Disk Access.
  2. Turn on **Maktaba**. If it is not in the list, click **+**, choose **Maktaba** in Applications and click **Open**.
  3. The server started before you turned it on still cannot read: choose **Pause server**, then **Resume server** in the menu bar.
- **Local Network** (required when you use Plex, a NAS by its address or other machines): lets the server reach devices on your network. macOS lists it as **maktaba-launcher**. The first time it reaches your network, macOS asks: choose **Allow**. If you chose **Don't Allow**: click **Open System Settings** (Privacy & Security → Local Network) and turn on **maktaba-launcher**. macOS does not tell apps whether this is on, so the item says **Cannot tell**; a Plex that does not answer with "no route to host" on [System health](/help/system-health#item-plex-livetv) is the sign it is off.
- **Run in the background** (required): the server's login item, so it starts when you log in. Setup turns it on at the end. If it is off: click **Open System Settings** (General → Login Items & Extensions) and turn on **Maktaba** under **Allow in the Background**. See [Starting at login](#login-item).
- **Keep this Mac awake** (optional): a Mac that sleeps stops serving, transcribing and airing channels until it wakes. Click **Open System Settings** (Energy) and turn on **Prevent automatic sleeping when the display is off**. On a laptop it is under Battery → Options, and works only on power.

[System health](/help/system-health#item-host-access) shows every library folder the server cannot read and why.

### Closing the window {#closing-the-window}

Closing the window only hides it. The server keeps running, and people on your network can keep watching. Open the window again with **Open Maktaba** in the menu bar.

On a TV or a big monitor, choose **View → Big Screen** for a full-screen view you drive with a remote or a game controller, and **View → Start in Big Screen** to open the app in it. See [Big screen](/help/big-screen).

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

### Maktaba cannot read a library folder {#full-disk-access}

- **What it means:** macOS does not let the server read the folder ("operation not permitted"): Maktaba has no Full Disk Access, or the server started before it was turned on.
- **Does Maktaba fix it itself?** No. Only you can allow it, in System Settings.
- **What you can do:** turn on Full Disk Access for Maktaba (see [Permissions](#permissions)), then choose **Pause server** and **Resume server** in the menu bar. If the folder is on a NAS, check that the share is mounted (it appears in Finder under Locations).

### Maktaba cannot reach Plex or a machine on the network {#local-network}

- **What it means:** connecting to an address on your network fails with "no route to host". On a Mac that usually means Maktaba is not allowed on the Local Network.
- **Does Maktaba fix it itself?** No. It tries again by itself once it is allowed.
- **What you can do:** open System Settings → Privacy & Security → Local Network and turn on **maktaba-launcher** (see [Permissions](#permissions)). If it is on, check that the other machine is on and on the same network.

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
