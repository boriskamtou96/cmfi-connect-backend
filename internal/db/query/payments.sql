-- name: CreatePayment :one
INSERT INTO payments (
    id, user_id, purpose, event_id, people, needs_lodging, departure_city,
    premium_plan, amount, currency, phone_number, provider
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetPayment :one
SELECT *
FROM payments
WHERE id = $1;

-- name: GetPaymentByRefundID :one
SELECT *
FROM payments
WHERE refund_id = $1;

-- name: MarkPaymentAccepted :exec
-- KPAY took it (its id given): the member is asked for their PIN.
UPDATE payments
SET status = 'accepted', provider_payment_id = @provider_payment_id, updated_at = now()
WHERE id = @id AND status = 'initiated';

-- name: GetPaymentByProviderID :one
SELECT *
FROM payments
WHERE provider_payment_id = $1 AND provider_payment_id <> '';

-- name: GetPaymentByProviderRefundID :one
SELECT *
FROM payments
WHERE provider_refund_id = $1 AND provider_refund_id <> '';

-- name: SetPaymentProviderRefundID :exec
UPDATE payments
SET provider_refund_id = @provider_refund_id, updated_at = now()
WHERE id = @id;

-- name: CompletePayment :one
-- The money arrived. No row when it was already known: done once.
UPDATE payments
SET status = 'completed',
    provider_transaction_id = @provider_transaction_id,
    checked_at = now(),
    updated_at = now()
WHERE id = @id AND status IN ('initiated', 'accepted')
RETURNING *;

-- name: FailPayment :execrows
UPDATE payments
SET status = 'failed',
    failure_code = @failure_code,
    checked_at = now(),
    updated_at = now()
WHERE id = @id AND status IN ('initiated', 'accepted');

-- name: TouchPaymentCheck :exec
-- Looked at, nothing new: the reconciler waits before looking again.
UPDATE payments
SET checked_at = now(), updated_at = now()
WHERE id = $1;

-- name: SettlePayment :exec
-- The completed payment did its work: the registration or the Premium.
UPDATE payments
SET settled_at = now(),
    premium_until = @premium_until,
    updated_at = now()
WHERE id = @id;

-- name: StartPaymentRefund :one
-- Only a completed payment, once.
UPDATE payments
SET status = 'refund_pending',
    refund_id = @refund_id,
    refund_reason = @refund_reason,
    failure_code = '',
    updated_at = now()
WHERE id = @id AND status IN ('completed', 'refund_failed')
RETURNING *;

-- name: CompletePaymentRefund :one
UPDATE payments
SET status = 'refunded', refunded_at = now(), checked_at = now(), updated_at = now()
WHERE refund_id = $1 AND status = 'refund_pending'
RETURNING *;

-- name: FailPaymentRefund :execrows
UPDATE payments
SET status = 'refund_failed',
    failure_code = @failure_code,
    checked_at = now(),
    updated_at = now()
WHERE refund_id = @refund_id AND status = 'refund_pending';

-- name: ListOpenPayments :many
-- What the reconciler checks: the answers still awaited from KPAY, and
-- the completed payments whose work is not done yet.
SELECT *
FROM payments
WHERE (status IN ('initiated', 'accepted', 'refund_pending')
       OR (status = 'completed' AND settled_at IS NULL))
  AND updated_at < @before
ORDER BY updated_at
LIMIT 50;

-- name: ListPayments :many
-- For the admins, latest first; event_id and purpose filter when given.
SELECT *
FROM payments
WHERE (sqlc.narg(event_id)::bigint IS NULL OR event_id = sqlc.narg(event_id))
  AND (@purpose::text = '' OR purpose = @purpose)
ORDER BY created_at DESC
LIMIT @lim OFFSET @off;

-- name: CountHeldEventPayments :one
-- Money kept for an event: it can't be deleted before they are refunded.
SELECT COUNT(*)::int
FROM payments
WHERE event_id = $1 AND status IN ('completed', 'refund_pending', 'refund_failed');

-- name: ListUserPremiumPayments :many
-- The Premium a member bought here, latest end first.
SELECT *
FROM payments
WHERE user_id = $1
  AND purpose = 'premium'
  AND status = 'completed'
  AND settled_at IS NOT NULL
ORDER BY premium_until DESC NULLS FIRST;

-- name: SetPaymentProviderID :exec
-- The id KPAY gave, learnt from its webhook when its first answer was lost.
UPDATE payments
SET provider_payment_id = @provider_payment_id, updated_at = now()
WHERE id = @id AND provider_payment_id = '';
