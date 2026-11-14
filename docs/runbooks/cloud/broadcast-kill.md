# Runbook: stop a broadcast now

> Epic 50 story 50.25. The kill switch and its break-glass.

## When

A valid copyright notice about something airing now, an urgent report, an abusive publisher,
or an incident on Maktaba's side; or a SEV2 alert that the edge refused a revocation list (a
bad signature or an unknown key).

## Check

1. The cloud's broadcast log ([broadcast events, last hour](https://admin.maktaba.tv/logs?service=cloud.api&q=revocation&last=1h);
   [edge errors](https://admin.maktaba.tv/logs?service=broadcast-edge&level=error&last=1h)):
   the `revocation_added` and `revocation_lifted` lines, the push to the edge, and any
   `revocation list refused` alert.
2. Is the cloud API up? The admin console's **Broadcast kill switch** page answers when it is.
3. What has to stop: one programme (a slate until a time), one channel, a publisher's account,
   a home, or everything.

## Do

1. **The API is up (the usual way).** Admin console → **Broadcast kill switch** → **Stop a
   broadcast**. Choose what it applies to, the action and the reason, and write the note for
   the audit. It needs a service admin and a fresh MFA check. The signed list reaches the edge
   at once (with a pull every minute as the backup), viewers stop within a minute, and the home
   is told.
2. **The API is down (break-glass), one channel.** In the Cloudflare dashboard, set the
   `broadcast-edge` Worker variable `BROADCAST_OFF_CHANNELS` to a comma-separated list of edge
   channel ids, or run `npx wrangler secret put BROADCAST_OFF_CHANNELS --env production` from
   `web/packages/broadcast-edge`. Viewers get `503 off_air` and ingest `403 revoked`, reason
   `incident`.
3. **The API is down, everything.** Set `BROADCAST_GLOBAL_OFF = 1` the same way.
4. **A refused list (SEV2).** Compare the edge's `CLOUD_PUBKEYS` with the cloud's
   `broadcast.key_path` key (`broadcast.PublicKeysJSON` prints it). A list signed by a key the
   edge does not hold means a key rotation was half done, or someone is forging lists: rotate
   per [secrets-rotation.md](secrets-rotation.md) if in doubt.
5. **Lifting.** The same page, **Lift**, with a note. A break-glass variable is cleared by hand
   once the API is back and the entry is on the signed list instead.

## Done when

The channel's viewers get `off_air` (or the slate), its home shows the channel as taken off by
Maktaba with the reason, and the entry is on the page with its audit row. After a break-glass:
the variables are empty again and the same stop is on the signed list.

## Tell users

The publisher sees the reason on their Broadcast page; nothing to post. For a global stop
during an incident, post on the status page per [status-page.md](status-page.md).
