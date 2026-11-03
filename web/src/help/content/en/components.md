# Components

Turn the parts of your home on and off, and see what each one is doing and why. Only administrators see this tab.

## What it is {#what-it-is}

**Settings → Components** ([open it](/settings/components)) lists every part of Maktaba on your home: thumbnails, transcription, Live TV, the language model, the models and tools they use, and the parts that are always on. Each part is in its group (Processing, Live TV, Models…), with a chip that says what it is doing (**Running**, **Off**, **Waiting**, **Finishing work**…) and why, in words.

The line under the title says which starting set your home is based on, for example **Based on Full, minus YouTube**.

The **Always on** group (the database, the server, playback, the library scan) has no switch: without those parts there is no home to set up.

## How to use it {#how-to-use}

### Turn a part on or off {#enable}

Use the part's **On / Off** switch. Off keeps the part's settings and its files on disk; it only stops it running. Turn it on again whenever you like.

Before anything changes, Maktaba works out what else the change touches. When the change touches only that part and nothing is waiting on it, it happens at once, and a message offers **Undo** for a few seconds. Otherwise a dialog says what happens, for example:

- **This also stops: Smart grouping, Book detection.** Parts that need it wait until it is back.
- **1,840 queued jobs will wait until you turn it back on. Nothing is lost.**
- **Jobs running now get up to 30 seconds to finish.**

Press **Cancel** and nothing changes.

### Stop a part until you turn it on {#stop}

**Stop** stops a part that is on, and keeps it stopped until you press **Start**. The row says **Stopped until you turn it on**. A stop has no end time: it stays through restarts and updates. New work for it still queues and waits.

Use Stop when you want a part out of the way for a while (for example transcription while you watch), and Off when you do not want it at all.

### Remove or install {#remove}

Open a part's details with **⋯**. **Remove** takes a part off the disk; its settings are kept. A part that other parts need cannot be removed: the dialog names them, and you turn them off first. **Install** puts back a part that was removed.

### See the details {#details}

**⋯** opens the part's details: what it needs and what needs it, its state on each machine with the checks each one reports, its version, how much room it takes on disk, and its last 20 changes with who made them.

### Several machines {#machines}

When your home has other machines, a row whose state differs on a machine says so (**2 of 3 machines**) with a **Machines** link to the [Machines](/admin/machines) page.

## What the states and messages mean {#states}

- **Running**: it is on and working. **Installed**: a model or tool is on the disk.
- **Off**: it is on the disk, turned off.
- **Stopped until you turn it on**: an admin pressed Stop; **Start** brings it back.
- **Waiting**: it is on, but something it needs is not, for example **Needs Language model, which is off.** It starts by itself when that is back.
- **Running with problems**: it works without something it could use, for example the Arabic second listen.
- **Starting**, **Finishing work**, **Installing**: it is on its way; the page follows it every few seconds.
- **Failed**: it could not run; the reason says why.
- **Not installed**: it is not on this home's disk.

## FAQ {#faq}

### Is queued work lost when I turn a part off? {#faq-lost}

No. Jobs already queued stay queued and wait. When you turn the part on again, it picks them up, and catches up on what arrived while it was off.

### What is the difference between Off and Stop? {#faq-off-stop}

Off says you do not want the part. Stop says you want it, just not now: it stays stopped until you press Start, and its work keeps queuing for it.

## When something goes wrong {#troubleshooting}

### Someone else changed this {#stale}

- **What it means:** another admin (or the command line) changed the same part after this page read it.
- **Does Maktaba fix it itself?** Yes: the page reloads the part and shows what it is now. Nothing you did was applied.
- **What you can do:** look at its new state, and change it again if you still want to.

### A part stays on Waiting {#waiting}

- **What it means:** something it needs is off, not installed, or not allowed by the licence; the chip says which.
- **Does Maktaba fix it itself?** Yes, as soon as what it needs is back.
- **What you can do:** turn on or install the part the chip names.

### The page says your home can't be reached {#unreachable}

- **What it means:** the page cannot reach the server. It keeps showing the last state it saw, greyed, and no switch works.
- **Does Maktaba fix it itself?** The page tries again every few seconds and carries on when the server answers.
- **What you can do:** check that the home is on and connected; see [System health](/help/system-health).
