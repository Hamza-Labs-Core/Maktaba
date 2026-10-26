# Storage & discovery

Tell the server where your video folders are and how devices find it. Only administrators see this tab.

## What it is {#what-it-is}

**Settings → Storage & discovery** ([open it](/settings/storage)) holds a few server settings that used to live only in the server's settings file:

- **Folder translations (path map)**: when your libraries store folders one way (for example the NAS's `/Volumes/Originals/...`) and this computer reaches them another way (for example `/Users/me/nas-nfs/...`).
- **Media folders**: the top folders that hold your videos. The folder picker on [Manage libraries](/help/library-management) starts from them.
- **Discovery**: lets phones, TVs and the Mac app on the same network find the server by themselves.
- **Addresses** and **public URLs**: where the server, the web app and streaming answer.

The table **Library folders from this computer** shows each library folder, where this computer finds it with the saved translations, and whether it is reachable.

## How to use it {#how-to-use}

### Change a setting {#change}

1. Change the fields you need. Add a row with **Add a translation** or **Add a media folder**; remove one with **Remove**.
2. Click **Save**. The server checks every value first. A value it refuses is shown in red under its field, and nothing is saved.
3. A banner says **Saved changes are waiting for a restart**.

### Restart the server {#restart}

Click **Restart the server**, then confirm. Videos stop for everyone for about a minute. The page waits until the server answers again, then reloads by itself.

If the button is not there, the server cannot restart itself: quit Maktaba and open it again (from the Mac app's menu, or the way you started it).

### Translate a folder {#path-map}

In **Library path**, type the start of the path as the library stores it, for example `/Volumes/Originals`. In **On this computer**, type where that same folder is here, for example `/Users/me/nas-nfs/Originals`. After the restart, check the table: the folders should say **Reachable**.

## What the states and messages mean {#states}

- **Saved changes are waiting for a restart**: the saved settings differ from the ones the server is running with.
- **Restarting…**: the server is starting again; the page reloads when it is back.
- **You changed an address**: devices and the Mac app may need the new address after the restart.
- **Reachable** / **Not reachable**: whether this computer can open that library folder.

## FAQ {#faq}

### Why does nothing change after I click Save? {#faq-restart}

The server reads these settings when it starts. Restart it to use them.

### What address format should I use? {#faq-address}

`host:port`, for example `:8080` (every network) or `127.0.0.1:8080` (this computer only). The server address and the web app address must differ. A public URL looks like `https://maktaba.example.com`, with no path after it.

## When something goes wrong {#troubleshooting}

### These settings are not available here {#not-available}

- **What it means:** this server was not started as the Maktaba server (for example it runs in Docker or alone), so the web cannot change its settings file.
- **Does Maktaba fix it itself?** No.
- **What you can do:** change the settings where that server is configured, then restart it.

### A field is refused when I save {#refused}

- **What it means:** the value is not valid: a folder that does not exist on this computer, a port already in use, an address in the wrong form.
- **Does Maktaba fix it itself?** No. Nothing is saved until every value is accepted.
- **What you can do:** read the message under the field, fix the value and save again.

### The server does not come back after a restart {#not-back}

- **What it means:** the server is taking longer than usual, or could not start with the new settings.
- **Does Maktaba fix it itself?** Sometimes: it may just need more time.
- **What you can do:** wait a minute and reload the page. If it still does not answer, open the Mac app and check its status, or see [System health](/help/system-health).
