-- +goose Up
-- A lost redeem response (story 25.6): the agent that redeemed a claim may redeem it again
-- within the claim's lifetime, proving itself with the retry key it sent the first time (its
-- SHA-256 here). NULL: redeemed by an agent that sends none, or the home has used its secret
-- since; never redeemable again.
ALTER TABLE server_claims ADD COLUMN IF NOT EXISTS retry_key_hash TEXT;
-- each use of a home's secret asks whether its claim can still be redeemed again
CREATE INDEX IF NOT EXISTS idx_server_claims_retry ON server_claims (used_server_id) WHERE retry_key_hash IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_server_claims_retry;
ALTER TABLE server_claims DROP COLUMN IF EXISTS retry_key_hash;
