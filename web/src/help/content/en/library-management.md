# Managing libraries

Add libraries, choose their folders, and keep them tidy. Only administrators see this page.

## What it is {#what-it-is}

A library is a name and one or more folders of videos. [Manage libraries](/admin/libraries) (**Administration → Libraries**) lists every library with:

- its **folders**, each marked **Reachable** or **Not reachable** (with the reason),
- how many videos it has, and how many are ready, still processing, failed or missing,
- when it was last scanned.

Removing or merging a library never deletes a file from your disk. Maktaba only forgets its own records.

## How to use it {#how-to-use}

### Add a library {#add}

1. Click **Add library**.
2. Type a name.
3. For each folder, type its path, or click **Browse…** to pick it: start from a place, open folders, use **Up** or the path at the top to go back, then click **Use this folder**.
4. Click **Add library**, then **Scan now** on its card to find its videos.

### Change a library {#edit}

Click **Edit** to rename it, add or remove folders, or open **Advanced settings**. The advanced settings are shown as JSON; leave them alone unless you know what each one does.

Two of them belong to **Convert to MP4**, and both are off unless you add them:

- `"convert": {"allow_reencode": true}` lets the library re-encode files whose video an MP4 cannot hold (VP9, AV1 and so on) to H.264. Re-encoding loses a little quality, so without it those files are skipped.
- `"convert": {"auto": true}` is kept for automatic conversion after a scan. No scan queues a conversion in this version, so start it from the library's menu on the Processing page.

See [How a conversion decides](/help/processing#convert-decides).

### YouTube sources {#youtube-sources}

Each card says where the library stands with YouTube, such as **YouTube: 1 source · off** or **YouTube: 3 sources · 1 on**. A library with one source has its **Downloads** switch right there: turning it on brings the source back in the mode it had when it was last on, or **New uploads and back catalogue** the first time.

A library with no source shows **Add YouTube source**, which opens the dialog below. **Look for its channel** reads the YouTube ids in the library's file names (`… #<id>.mp4`, `… [<id>].mp4`) and its `.info.json` files, and matches them with what Maktaba already knows of YouTube; it asks YouTube nothing. When it finds one, the card says **Looks like channel …**, and **Add as source** adds it, off. When another library already follows that channel, or a library with a source has a name like this one's, the card says so and offers **Merge into …** (see [Merge](#merge)).

Maktaba also finds each library's sources by itself, from the videos it has linked to YouTube (their `.info.json` files, or their titles). When most of a library's linked videos sit in real YouTube playlists that no other library's videos make up, those playlists become its sources; otherwise the channel most of them come from does. So several libraries of one channel, a playlist each, each get their own playlist. A source found this way is added **off**, says **Found from its linked videos**, and nothing about the sources already there changes. A channel or playlist is never the source of two libraries: when a library's videos come from one another library already follows, its card says so and offers **Merge into …** instead. **Look for its channel** looks at the linked videos first, so it gives the same answer.

Click **YouTube sources…** on a library's card to see the YouTube channels and playlists it follows, add one by its link, turn one on or off, and set how it downloads. It is the same dialog as **YouTube sources…** in the library's menu on the Processing page. The channels a library already had start **Off**: nothing downloads until you set a source to **New uploads and back catalogue** or **New uploads only**. See [YouTube sources](/help/processing#youtube-sources) for the modes and options.

### Import from Tartube {#tartube}

**Import from Tartube…** at the top of the page makes the YouTube sources of every library at once from Tartube's records. The path of Tartube's database (`tartube.db`) or its JSON export is filled in when Maktaba finds one beside the library folders; change it if it is elsewhere, then press **Import**.

Each channel and playlist Tartube downloads into a folder that is a library's folder becomes a source of that library, **off**, and every video Tartube downloaded there counts as downloaded, so it is never fetched again. Running it again changes nothing that is already there, and a source a library already has keeps its mode. The report lists what was **Added (off)**, what was **Already there**, what was **Skipped** and why (a search page, another site such as x.com, a link that is no channel or playlist), and the Tartube channels with no library: **Not in Maktaba yet**. No library is made: add one for its folder, then import again.

### Scan {#scan}

**Scan now** looks for new, changed and missing files right away. A bar shows the scan while it runs, even if you reload the page.

### Empty trash {#empty-trash}

A video whose file was deleted or moved is marked **missing**. **Empty trash** removes the missing videos of a library, with their transcripts and watch history. Use it after you have cleaned up a folder on purpose.

### Fix folders claimed twice {#duplicates}

When two libraries hold the same folder (or one's folder is inside the other's), the panel **Folders claimed twice** shows them. The library with the most videos is marked **Keeps the videos**. Click **Merge into …** on the other one.

### Merge {#merge}

**Merge…** moves every video of a library into another library, with its transcripts and watch history. A video the other library already has is not kept twice. The merged library's folders are added to the other one, and the merged library is removed. Type the library's name to confirm.

### Remove {#remove}

**Remove…** shows what goes with the library (videos, transcripts, resume points, watch records, series, collections, Live TV blocks). Type the library's name to confirm. The files stay on your disk; add the folder again later to bring them back (they are processed again).

## What the states and messages mean {#states}

- **Reachable**: this computer can open the folder.
- **Not reachable: not-found**: the folder does not exist here. The disk may be unplugged, or the path needs a translation in [Storage & discovery](/help/storage).
- **Same folder** / **Folder inside another**: another library holds this folder too.
- **missing**: the video's file is no longer in its folder.
- **Never scanned**: the library has not been scanned yet.

## FAQ {#faq}

### Will removing a library delete my videos? {#faq-delete}

No. The files stay where they are. Only Maktaba's records of them go.

### Why do I see a path I did not type? {#faq-paths}

Maktaba stores each folder the way the library knows it. **On this computer** shows where this machine finds it, using the translations in [Storage & discovery](/help/storage).

## When something goes wrong {#troubleshooting}

### A folder is refused when I add it {#folder-refused}

- **What it means:** the server cannot open that folder, or another library already holds it. The reason is shown under the folder.
- **Does Maktaba fix it itself?** No.
- **What you can do:** check the disk is connected and the path is right, or pick the folder with **Browse…**. If the library stores paths of another computer, add a translation in [Storage & discovery](/help/storage).

### Merge or remove does nothing {#confirm-name}

- **What it means:** the name you typed does not match the library's name exactly.
- **Does Maktaba fix it itself?** No.
- **What you can do:** type the name exactly as shown, including capitals and spaces.

### Something went wrong (with Try again) {#load-failed}

- **What it means:** the list could not be loaded from the server.
- **Does Maktaba fix it itself?** No. The page waits for you.
- **What you can do:** click **Try again**. If it keeps failing, check [System health](/help/system-health).
