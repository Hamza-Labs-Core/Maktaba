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
