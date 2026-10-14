-- ----- blocks: a member stops another one's invitations -----

-- name: BlockMember :exec
INSERT INTO member_blocks (blocker_id, blocked_id)
VALUES (@blocker_id, @blocked_id)
ON CONFLICT DO NOTHING;

-- name: UnblockMember :exec
DELETE FROM member_blocks
WHERE blocker_id = @blocker_id
  AND blocked_id = @blocked_id;

-- name: ListBlockedIDs :many
-- The members [blocker_id] blocked, the latest first.
SELECT blocked_id
FROM member_blocks
WHERE blocker_id = @blocker_id
ORDER BY created_at DESC;

-- name: ListBlockersAmong :many
-- Who among [user_ids] blocked [blocked_id]: no invitation from them.
SELECT blocker_id
FROM member_blocks
WHERE blocked_id = @blocked_id
  AND blocker_id = ANY(@user_ids::bigint[]);

-- name: DropInvitationsFrom :exec
-- Blocking: the invitations of [blocked_id] to [blocker_id] not answered yet go.
DELETE FROM importunity_members
WHERE user_id = @blocker_id
  AND invited_by = @blocked_id
  AND status = 'invited';

-- ----- reports, handled in the back office -----

-- name: CreateReport :one
INSERT INTO member_reports (reporter_id, reported_id, importunity_id, importunity_title, reason, comment)
VALUES (@reporter_id, @reported_id, sqlc.narg(importunity_id), @importunity_title, @reason, @comment)
RETURNING id;

-- name: ListReports :many
-- [status] empty for all; the newest first. With the role of the reported
-- member and how many open reports are about them.
SELECT r.id,
       r.reporter_id,
       r.reported_id,
       r.importunity_id,
       r.importunity_title,
       r.reason,
       r.comment,
       r.status,
       r.created_at,
       r.resolved_at,
       u.role AS reported_role,
       (SELECT count(*)
        FROM member_reports o
        WHERE o.reported_id = r.reported_id
          AND o.status = 'open')::int AS open_about_reported
FROM member_reports r
JOIN users u ON u.id = r.reported_id
WHERE (@status::text = '' OR r.status = @status::text)
ORDER BY r.created_at DESC, r.id DESC
LIMIT @lim OFFSET @off;

-- name: CountReports :one
SELECT count(*)
FROM member_reports
WHERE (@status::text = '' OR status = @status::text);

-- name: ResolveReport :one
UPDATE member_reports
SET status = 'resolved',
    resolved_at = COALESCE(resolved_at, now()),
    resolved_by = COALESCE(resolved_by, @resolved_by)
WHERE id = @id
RETURNING id;

-- name: GetReport :one
-- One report, as ListReports gives it.
SELECT r.id,
       r.reporter_id,
       r.reported_id,
       r.importunity_id,
       r.importunity_title,
       r.reason,
       r.comment,
       r.status,
       r.created_at,
       r.resolved_at,
       u.role AS reported_role,
       (SELECT count(*)
        FROM member_reports o
        WHERE o.reported_id = r.reported_id
          AND o.status = 'open')::int AS open_about_reported
FROM member_reports r
JOIN users u ON u.id = r.reported_id
WHERE r.id = @id;
