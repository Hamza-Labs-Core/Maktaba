# Runbook: payment webhooks fail or lag

> Epic 50 story 50.12, Epic 25 (25.14). The payment provider is not chosen yet; this runbook is written against any provider that retries its webhooks.

## When

Webhook failures or lag from the payment provider (its dashboard, or the cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines)'s `billing` group).

## Check

1. The cloud's logs and error groups in the service admin's cloud log view (Epic 53.17; the witness keeps a dead node's last lines): the saved search `billing webhooks, last 24 h`, by error.
2. The provider's status page and its webhook log (failed deliveries and their answers).

## Do

1. Fix the cause (a deploy: [deploy-rollback.md](deploy-rollback.md); a rotated signing secret: [secrets-rotation.md](secrets-rotation.md)).
2. Replay the failed events from the provider's dashboard: the handler is idempotent (each event id once).
3. Reconcile: compare the provider's active subscriptions with the accounts' plans in the service admin view (Accounts, filtered by plan). The daily reconciliation job (25.14) does this once it is built; until then, by hand.

## Done when

The provider's webhook log shows no failures for an hour, and every paying subscription matches its account's plan.

## Tell users

Nothing, unless plans changed wrongly: then email the accounts whose plan was wrong.
