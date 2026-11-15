# Runbook: an urgent report about a broadcast

> Epic 50 story 50.24 §4. A viewer reported illegal content on a published channel (or an urgent
> copyright notice arrived). The target to a decision is one hour.

## When

The SEV2 alert `broadcast_urgent_report` fired: the api role logged an error line with code
`moderation_urgent_report` for a report in the category *illegal content*, or for a notice marked
urgent.

## Check

1. The report's line in the cloud's log ([urgent reports, last hour](https://admin.maktaba.tv/logs?service=cloud.api&q=moderation_urgent_report&last=1h)):
   its `report_id` or `notice_id`.
2. Admin console → **Moderation**: the report is at the top of the queue, flagged urgent, with the
   channel's address, the time and the reporter's text (and email, if they gave one).
3. Open the channel's address on `watch.maktaba.tv` and look at what is on air now, and at the
   guide around the reported time.

## Do

1. **Clearly illegal and on air now.** On the report, **Take down**: scope *channel* (the channel
   goes off air within a minute through the kill switch) or *item* with **On air now** (the edge
   shows the slate within a minute, until the home confirms the takedown). Write the reason. It
   needs a fresh MFA check.
2. **An order from an authority**, or something you saw yourself: the same action with the basis
   *authority* or *admin knowledge*.
3. **Not illegal.** **Dismiss** with the reason. A copyright report is handled as a notice (see
   [broadcast-takedown.md](broadcast-takedown.md)).
4. **Something the law requires reporting** to an authority: follow counsel's instructions; the
   records export (**Export**, CSV or JSON) gives them every record with who and when.

## Done when

The report is no longer open in the queue: actioned (the action shows its home state and the
kill-switch entry) or dismissed, each with its reason in the audit.

## Tell users

Nothing to post. The publisher sees the statement of reasons on their Broadcast page and by email;
the reporter is not answered beyond the form's thanks.
