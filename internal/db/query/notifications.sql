-- ----- phones that receive push notifications -----

-- name: UpsertDevice :one
-- A token already known moves to the member signed in now on that phone.
INSERT INTO devices (user_id, token, platform, language)
VALUES ($1, $2, $3, $4)
ON CONFLICT (token) DO UPDATE
SET user_id = EXCLUDED.user_id,
    platform = EXCLUDED.platform,
    language = EXCLUDED.language,
    updated_at = now()
RETURNING *;

-- name: DeleteDevice :execrows
DELETE FROM devices
WHERE token = $1
  AND user_id = $2;

-- name: DeleteDevicesByTokens :exec
-- The tokens Firebase no longer knows (app removed, token replaced).
DELETE FROM devices
WHERE token = ANY(@tokens::text[]);

-- name: ListDeviceTokensOfUsers :many
SELECT token
FROM devices
WHERE user_id = ANY(@user_ids::bigint[])
ORDER BY id;

-- name: ListDevicesOfUsers :many
-- The phones of members with their language, for a push written by the
-- server (each phone reads it in its language).
SELECT token, language
FROM devices
WHERE user_id = ANY(@user_ids::bigint[])
ORDER BY id;

-- name: CountDevicesOfUsers :one
SELECT count(*)::int
FROM devices
WHERE user_id = ANY(@user_ids::bigint[]);

-- ----- announcements written by the admins -----

-- name: ListAudienceMembers :many
-- Every member with the place of their profile (empty without profile): the
-- server groups the values typed by hand ("yaounde", "Yaoundé ").
SELECT u.id,
       COALESCE(p.country, '')::text AS country,
       COALESCE(p.city, '')::text AS city,
       COALESCE(p.church, '')::text AS church,
       COALESCE(p.assembly, '')::text AS assembly
FROM users u
LEFT JOIN profiles p ON p.user_id = u.id
ORDER BY u.id;

-- name: CreateAnnouncement :one
INSERT INTO announcements (title, body, audience, author_id, recipients_count)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateAnnouncementAttachment :exec
INSERT INTO announcement_attachments (announcement_id, position, media_id, name)
VALUES ($1, $2, $3, $4);

-- name: AddAnnouncementRecipients :exec
INSERT INTO announcement_recipients (announcement_id, user_id)
SELECT @announcement_id::bigint, unnest(@user_ids::bigint[]);

-- name: SetAnnouncementPushed :exec
UPDATE announcements
SET pushed_count = $2
WHERE id = $1;

-- name: GetAnnouncement :one
SELECT sqlc.embed(a), COALESCE(TRIM(CONCAT_WS(' ', u.first_name, u.last_name)), '')::text AS author
FROM announcements a
LEFT JOIN users u ON u.id = a.author_id
WHERE a.id = $1;

-- name: ListAnnouncements :many
-- For the admins: the latest first.
SELECT sqlc.embed(a), COALESCE(TRIM(CONCAT_WS(' ', u.first_name, u.last_name)), '')::text AS author
FROM announcements a
LEFT JOIN users u ON u.id = a.author_id
ORDER BY a.created_at DESC, a.id DESC
LIMIT @lim OFFSET @off;

-- name: CountAnnouncements :one
SELECT count(*)
FROM announcements;

-- name: ListAnnouncementAttachments :many
SELECT a.announcement_id, a.position, a.name, sqlc.embed(m)
FROM announcement_attachments a
JOIN media m ON m.id = a.media_id
WHERE a.announcement_id = ANY(@ids::bigint[])
ORDER BY a.announcement_id, a.position;

-- name: CountAnnouncementReads :many
SELECT announcement_id, count(*)::int AS readers
FROM announcement_recipients
WHERE announcement_id = ANY(@ids::bigint[])
  AND read_at IS NOT NULL
GROUP BY announcement_id;

-- ----- a member's notifications -----

-- name: ListMyAnnouncements :many
SELECT sqlc.embed(a), r.read_at
FROM announcement_recipients r
JOIN announcements a ON a.id = r.announcement_id
WHERE r.user_id = @user_id
ORDER BY a.created_at DESC, a.id DESC
LIMIT @lim OFFSET @off;

-- name: CountMyAnnouncements :one
SELECT count(*)::int AS total,
       (count(*) FILTER (WHERE read_at IS NULL))::int AS unread
FROM announcement_recipients
WHERE user_id = $1;

-- name: GetMyAnnouncement :one
SELECT sqlc.embed(a), r.read_at
FROM announcement_recipients r
JOIN announcements a ON a.id = r.announcement_id
WHERE r.user_id = @user_id
  AND a.id = @id;

-- name: MarkAnnouncementRead :exec
UPDATE announcement_recipients
SET read_at = now()
WHERE announcement_id = @id
  AND user_id = @user_id
  AND read_at IS NULL;
