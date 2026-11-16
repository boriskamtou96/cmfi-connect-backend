-- name: SavePremiumRenewal :one
-- Asked with a paid plan: the requests start again for its new end.
INSERT INTO premium_renewals (user_id, plan, phone_number, provider, period_end)
VALUES (@user_id, @plan, @phone_number, @provider, @period_end)
ON CONFLICT (user_id) DO UPDATE
SET plan = EXCLUDED.plan,
    phone_number = EXCLUDED.phone_number,
    provider = EXCLUDED.provider,
    period_end = EXCLUDED.period_end,
    attempts = 0,
    last_attempt_at = NULL,
    last_payment_id = NULL,
    updated_at = now()
RETURNING *;

-- name: GetPremiumRenewal :one
SELECT *
FROM premium_renewals
WHERE user_id = $1;

-- name: DeletePremiumRenewal :execrows
DELETE FROM premium_renewals
WHERE user_id = $1;

-- name: ListDuePremiumRenewals :many
-- Those whose Premium ends before [before]: the pass looks at them.
SELECT *
FROM premium_renewals
WHERE period_end < @before
ORDER BY period_end
LIMIT 100;

-- name: RestartPremiumRenewal :exec
-- Premium got a new end (renewed, or paid by hand): no request yet for it.
UPDATE premium_renewals
SET period_end = @period_end,
    attempts = 0,
    last_attempt_at = NULL,
    last_payment_id = NULL,
    updated_at = now()
WHERE user_id = @user_id;

-- name: RecordPremiumRenewalAttempt :exec
UPDATE premium_renewals
SET attempts = attempts + 1,
    last_attempt_at = now(),
    last_payment_id = @last_payment_id,
    updated_at = now()
WHERE user_id = @user_id;
