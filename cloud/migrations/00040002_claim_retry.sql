-- +goose Up
-- A lost redeem response (story 25.6): the agent that redeemed a claim may redeem it again
-- within the claim's lifetime, proving itself with the retry key it sent the first time (its
-- SHA-256 here). NULL: redeemed by an agent that sends none, and never redeemable again.
ALTER TABLE server_claims ADD COLUMN IF NOT EXISTS retry_key_hash TEXT;

-- +goose Down
ALTER TABLE server_claims DROP COLUMN IF EXISTS retry_key_hash;
