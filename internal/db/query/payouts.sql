-- name: SetEventPayoutAccount :one
-- A new account is not confirmed until its test arrives.
UPDATE events
SET payout_phone = @payout_phone,
    payout_provider = @payout_provider,
    payout_holder = @payout_holder,
    payout_confirmed_at = NULL,
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: ConfirmEventPayoutAccount :exec
-- Only when the account is still the one tested.
UPDATE events
SET payout_confirmed_at = now()
WHERE id = @id AND payout_phone = @payout_phone AND payout_provider = @payout_provider;

-- name: CreatePayout :one
INSERT INTO payouts (id, event_id, kind, amount, currency, phone_number, provider, holder, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetPayout :one
SELECT *
FROM payouts
WHERE id = $1;

-- name: GetPayoutByProviderID :one
SELECT *
FROM payouts
WHERE provider_payout_id = $1 AND provider_payout_id <> '';

-- name: MarkPayoutPending :exec
UPDATE payouts
SET status = 'pending', provider_payout_id = @provider_payout_id, updated_at = now()
WHERE id = @id AND status = 'initiated';

-- name: CompletePayout :one
-- No row when it was already known: done once.
UPDATE payouts
SET status = 'completed', completed_at = now(), updated_at = now()
WHERE id = $1 AND status IN ('initiated', 'pending')
RETURNING *;

-- name: FailPayout :one
UPDATE payouts
SET status = 'failed', failure_code = @failure_code, updated_at = now()
WHERE id = @id AND status IN ('initiated', 'pending')
RETURNING *;

-- name: ListEventPayouts :many
SELECT *
FROM payouts
WHERE event_id = $1
ORDER BY created_at DESC
LIMIT 50;

-- name: ListPaymentsToForward :many
-- The money of an event still in the wallet: its paid registrations not sent
-- yet, oldest first.
SELECT *
FROM payments
WHERE event_id = $1
  AND purpose = 'event'
  AND status = 'completed'
  AND settled_at IS NOT NULL
  AND payout_id IS NULL
ORDER BY created_at;

-- name: LinkPaymentsToPayout :exec
UPDATE payments
SET payout_id = @payout_id, updated_at = now()
WHERE id = ANY(@ids::uuid[]) AND payout_id IS NULL;

-- name: UnlinkPayoutPayments :exec
-- The sending failed: its payments wait for the next one.
UPDATE payments
SET payout_id = NULL, updated_at = now()
WHERE payout_id = $1;
