-- name: CreatePremiumOffer :one
INSERT INTO premium_offers (months, audience, note, notify, members_count, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: CreatePremiumGifts :exec
INSERT INTO premium_gifts (offer_id, user_id, months)
SELECT @offer_id, unnest(@user_ids::bigint[]), @months
ON CONFLICT DO NOTHING;

-- name: GetPremiumOffer :one
SELECT *
FROM premium_offers
WHERE id = $1;

-- name: ListPremiumOffers :many
-- Latest first, with how far their gifts went.
SELECT o.*,
       COUNT(g.id) FILTER (WHERE g.status = 'given')::int AS given,
       COUNT(g.id) FILTER (WHERE g.status = 'pending')::int AS pending,
       COUNT(g.id) FILTER (WHERE g.status = 'failed')::int AS failed
FROM premium_offers o
LEFT JOIN premium_gifts g ON g.offer_id = o.id
GROUP BY o.id
ORDER BY o.created_at DESC
LIMIT @lim OFFSET @off;

-- name: ListPendingGifts :many
-- The gifts to give, of one offer (offer_id) or of all of them, oldest
-- first; the failed ones wait a minute before another attempt.
SELECT *
FROM premium_gifts
WHERE status = 'pending'
  AND (sqlc.narg(offer_id)::bigint IS NULL OR offer_id = sqlc.narg(offer_id))
  AND (attempts = 0 OR updated_at < now() - interval '1 minute')
ORDER BY id
LIMIT @lim;

-- name: GiveGift :exec
UPDATE premium_gifts
SET status = 'given', until = @until, updated_at = now()
WHERE id = @id AND status = 'pending';

-- name: GiftAttemptFailed :exec
-- After 5 attempts it stays failed.
UPDATE premium_gifts
SET attempts = attempts + 1,
    status = CASE WHEN attempts + 1 >= 5 THEN 'failed' ELSE status END,
    updated_at = now()
WHERE id = $1 AND status = 'pending';

-- name: ListUserGivenGifts :many
SELECT *
FROM premium_gifts
WHERE user_id = $1 AND status = 'given';

-- name: RevokePremiumOffer :many
-- Taken back: its gifts go (given ones are taken back in RevenueCat by the
-- caller), and the members they were given to are answered.
WITH offer AS (
    UPDATE premium_offers
    SET revoked_at = now()
    WHERE premium_offers.id = $1 AND revoked_at IS NULL
    RETURNING premium_offers.id
)
UPDATE premium_gifts
SET status = 'revoked', updated_at = now()
WHERE offer_id IN (SELECT id FROM offer) AND status IN ('pending', 'given', 'failed')
RETURNING user_id, status;
