# Runbook: a copyright notice, a counter-notice, a restore

> Epic 50 story 50.24 §2, §3. Maktaba Cloud is a host: it acts on notices about what its users
> publish. The target is 24 hours from a valid notice to a decision.

## When

A notice is open in the queue (from the form on `maktaba.tv/legal/copyright`, or an email to
`copyright@maktaba.tv` that a service admin entered), a counter-notice's restore window opened, or
a claimant wrote that they went to court or withdrew their notice.

## Check

1. The queue's log lines ([moderation events, last day](https://admin.maktaba.tv/logs?service=cloud.api&q=moderation_&last=24h)):
   `moderation_notice`, `moderation_takedown`, `moderation_home_ack`, `moderation_counter_notice`.
2. Admin console → **Moderation**: the notice's claimant, work, addresses, time, explanation and
   statements; what the address resolved to (the home and channel); how many of this notifier's
   notices were manifestly unfounded.
3. The channel's guide at the notice's time on `watch.maktaba.tv`.

## Do

1. **A valid notice.** **Take down** with the reason. The default scope is the *item* (the
   programme at the notice's time, or the item named); a *source* when the notice names a source or
   a whole channel of it; *channel* or *publisher* only for flagrant cases (the kill switch). Tick
   **On air now** when the item is airing: the edge shows the slate within a minute until the home
   confirms the takedown and says when the slot ends. The home receives a signed instruction and
   removes the item's publishable mark; its slot plays the slate and the scheduler replaces it.
   The publisher gets the statement of reasons in English and Arabic, and the notice is a strike.
2. **An incomplete notice** never reaches the queue: the form answered with what was missing. An
   emailed one is entered only when complete.
3. **Not valid, or about another site.** **Reject** with the reason; tick *manifestly unfounded*
   when it is. A notifier with repeated unfounded notices can be suspended from the form
   (**Suspend notifier**, with the reason).
4. **The home is offline.** Nothing to do: the instruction waits on the cloud link and the edge's
   slate holds; the home applies it when it returns, before its next schedule top-up.
5. **A counter-notice.** The claimant was sent a copy. The queue shows the restore window (10
   business days). If the claimant writes that they went to court, **Lawsuit** on the
   counter-notice. Otherwise, once the window opens, **Restore** with the reason: the home
   re-enables the item through the same signed channel, and the strike is withdrawn.
6. **The claimant withdraws the notice.** **Withdraw** on the notice (its strike goes), then
   **Restore**.
7. **Three active strikes.** Broadcasting ends for the publisher on its own: a `terminate` entry
   takes every channel off air and the log line `moderation_terminated` asks the owner to set
   Broadcaster to cancel at the period's end at the payment provider. An appeal comes by email;
   **Reinstate** with the reason, and withdraw a strike if the appeal shows one was wrong.

## Done when

The notice is accepted, rejected or withdrawn; an accepted one's action shows the home state
*applied* (or *not found*: the item was already gone, and the strike stands) and, when it was on
air, the slate entry ends with the item's slot.

## Tell users

The publisher is told by the statement of reasons (Broadcast page and email). The claimant gets a
copy of any counter-notice. Nothing to post.
