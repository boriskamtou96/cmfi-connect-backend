-- name: ListSendableAuthorities :many
-- The authorities of [user_id] who can receive a report in the app: members
-- who accepted the link, and to whom the reports are shared.
SELECT id, linked_user_id
FROM authorities
WHERE user_id = $1
  AND linked_user_id IS NOT NULL
  AND status = 'accepted'
  AND shares_regularity;

-- name: UpsertReportSending :one
-- Sent again for the same period: new again (unread), the answer kept.
INSERT INTO report_sendings (authority_id, disciple_id, receiver_id, period_from, period_to)
VALUES (sqlc.arg(authority_id), sqlc.arg(disciple_id), sqlc.arg(receiver_id), sqlc.arg(period_from), sqlc.arg(period_to))
ON CONFLICT (authority_id, period_from, period_to)
DO UPDATE SET sent_at = now(), read_at = NULL
RETURNING *;

-- name: ListReceivedReportSendings :many
SELECT * FROM report_sendings
WHERE receiver_id = $1
ORDER BY sent_at DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: CountReceivedReportSendings :one
SELECT count(*) FROM report_sendings WHERE receiver_id = $1;

-- name: ListSentReportSendings :many
SELECT * FROM report_sendings
WHERE disciple_id = $1
ORDER BY sent_at DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: CountSentReportSendings :one
SELECT count(*) FROM report_sendings WHERE disciple_id = $1;

-- name: MarkReportSendingRead :one
-- Only its receiver; the first reading stays.
UPDATE report_sendings
SET read_at = COALESCE(read_at, now())
WHERE id = sqlc.arg(id) AND receiver_id = sqlc.arg(receiver_id)
RETURNING *;

-- name: ReplyReportSending :one
UPDATE report_sendings
SET reply = sqlc.arg(reply), replied_at = now(), read_at = COALESCE(read_at, now())
WHERE id = sqlc.arg(id) AND receiver_id = sqlc.arg(receiver_id)
RETURNING *;
