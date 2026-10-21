# Filler pools

The short clips, bumpers and station idents that fill the gaps between programmes on Maktaba's channels.

## What it is {#what-it-is}

A programme rarely ends exactly when its slot does. Maktaba fills the gap with clips from **filler pools**. A pool is a named list of videos from your library, each marked as **Filler**, **Bumper** or **Station ID**. Channels pick from these pools in their **Filler** settings.

You manage pools on **Filler pools** (Admin, at [/admin/filler](/admin/filler)), or from the **Filler pools** link on the [Channels](/help/channels) page. Only admins can open it.

## How to use it {#how-to-use}

### Create a pool {#create-pool}

1. Type a **Pool name**.
2. Click **Add pool**. The new pool opens so you can add clips.

### Add and remove clips {#add-clips}

1. Click a pool's name to open it. The header shows how many clips it has and their total length.
2. Choose the **Kind** for the next clip: **Filler**, **Bumper** or **Station ID**.
3. In **Add a clip from the library**, type to search and pick a video. It is added with the kind you chose.
4. To remove a clip, click the ✕ next to it. Click a clip's title to watch it.

**Search pools** narrows the list by name. **Delete** removes a whole pool after you confirm.

### The three kinds {#kinds}

- **Filler**: a general clip that fills time.
- **Bumper**: a short piece around a programme break.
- **Station ID**: a short channel ident.

### Use a pool on a channel {#use-in-channel}

Pools do nothing until a channel uses them. In the channel editor:

- On the **Filler** tab, **Add a filler list** picks a pool, with a **Weight** (how often it is picked compared with the other lists) and a **Cooldown (minutes)**. See [Channels](/help/channels#filler).
- On a slot of the **Schedule**, **Filler pool for this slot** makes that slot's gaps take clips from this pool first. See [Channels](/help/channels#slots).

A gap is filled from the slot's pool first, then from the channel's filler lists. With nothing to pick from, the channel shows a still screen (the slate).

### Pools from the lineup plan {#plan-pools}

Pools marked **From plan** are written by the lineup plan. The next lineup run replaces their clips with the plan's, so a change you make by hand there does not last. To keep your own clips, make your own pool.

### Edits go into a TV change {#tv-draft}

When TV staging is on, creating a pool, deleting one, or adding or removing a clip does not go on the air at once. The page says "Saved to draft change #n: it goes on the air when the change is approved", and the pool shows **In draft change #n** (click it to open the change). See [TV changes](/help/tv-changes).

## What the states and messages mean {#states}

- **From plan**: the lineup plan writes this pool; its clips are replaced on the next lineup run.
- **One channel**: the pool belongs to a single channel.
- **In draft change #n**: this pool has changes waiting in that TV change.
- **Not on the air yet**: a clip you added that waits for its TV change to be approved.
- **No items yet.**: the pool is empty and adds nothing to a gap.
- **No filler pools**: there are no pools yet. Create one, then add clips.

## FAQ {#faq}

### Why does my channel show a still screen between programmes? {#faq-slate}

The channel has no filler to pick from: no filler lists, no pool on the slot, or only empty pools. Add clips to a pool and add that pool to the channel's **Filler** tab.

### Why did the clips I added to a pool disappear? {#faq-plan-replaced}

The pool is **From plan**, and the lineup run replaced its clips with the plan's. Use a pool of your own instead.

### Can one pool serve several channels? {#faq-shared}

Yes. Several channels can add the same pool to their filler lists. A pool marked **One channel** belongs to a single channel.

### Why do I see the same clip too often? {#faq-repeat}

The pool may be small. Add more clips, or raise **Clip repeat cooldown (minutes)** on the channel's **Filler** tab.

## When something goes wrong {#troubleshooting}

### The page shows an error instead of the pools {#load-error}

- **What it means:** Maktaba could not load the pools.
- **Does Maktaba fix it itself?** No. Nothing is lost.
- **What you can do:** click **Try again**. If it keeps failing, check [System health](/help/system-health).

### Pool not found (in a channel's Filler tab) {#pool-not-found}

- **What it means:** the channel uses a pool that was deleted.
- **Does Maktaba fix it itself?** No. That list adds no clips.
- **What you can do:** open the channel, remove that list on the **Filler** tab and add another pool.

### A clip cannot be added or removed {#item-failed}

- **What it means:** the save failed; the message names the reason (for example, the pool or the clip is already gone).
- **Does Maktaba fix it itself?** No.
- **What you can do:** reload the page and try again.

### My change is not on the air {#not-on-air}

- **What it means:** TV staging is on and your change waits in a draft TV change.
- **Does Maktaba fix it itself?** No. It waits for the change to be checked and approved.
- **What you can do:** open the change from **In draft change #n** and follow [TV changes](/help/tv-changes#approve).
