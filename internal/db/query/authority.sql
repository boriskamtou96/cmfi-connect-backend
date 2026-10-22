-- name: CreateUserAuthority :one
INSERT INTO authorities (first_name, last_name, phone_number, email, is_disciple_maker, user_id, linked_user_id, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetUserAuthorities :many
SELECT *
FROM authorities
WHERE user_id = $1
ORDER BY id DESC
LIMIT $2
OFFSET $3;

-- name: GetAuthorityById :one
SELECT *
FROM authorities
WHERE id = $1
LIMIT 1;

-- name: DeleteAuthority :exec
DELETE FROM authorities
WHERE id = $1 AND user_id = $2;

-- name: CountUserAuthorities :one
SELECT COUNT(*)
FROM authorities
WHERE user_id = $1;

-- name: UpdateAuthority :one
UPDATE authorities
SET first_name = COALESCE($2, first_name),
    last_name = COALESCE($3, last_name),
    phone_number = COALESCE($4, phone_number),
    email = COALESCE($5, email),
    is_disciple_maker = COALESCE($6, is_disciple_maker)
WHERE user_id = $1
RETURNING *;

-- name: ListAuthorityRequests :many
-- Members who asked [linked_user_id] to accompany them, waiting for an answer.
SELECT *
FROM authorities
WHERE linked_user_id = $1 AND status = 'pending'
ORDER BY created_at DESC;

-- name: AnswerAuthorityRequest :one
UPDATE authorities
SET status = $3
WHERE id = $1 AND linked_user_id = $2 AND status = 'pending'
RETURNING *;

-- name: SetAuthoritySharing :one
-- Whether [user_id] shares their reports with this authority (disciple maker or not).
UPDATE authorities
SET shares_regularity = $3
WHERE id = $1 AND user_id = $2
RETURNING *;

-- name: ListDisciples :many
-- The members who chose [member_id] as one of their authorities and whose link
-- they accepted: those who chose them as disciple maker first, then by name
-- (the profile's first, like the member cards). total_items counts the items
-- each one can fill in, as their day form lists them.
SELECT a.id, a.user_id, a.is_disciple_maker, a.shares_regularity,
       (SELECT COUNT(*)
        FROM activity_types at
        WHERE (at.user_id IS NULL OR at.user_id = a.user_id)
          AND at.archived_at IS NULL) AS total_items
FROM authorities a
JOIN users u ON u.id = a.user_id
LEFT JOIN profiles p ON p.user_id = u.id
WHERE a.linked_user_id = @member_id::bigint
  AND a.status = 'accepted'
ORDER BY a.is_disciple_maker DESC,
         COALESCE(NULLIF(p.first_name, ''), u.first_name),
         COALESCE(NULLIF(p.last_name, ''), u.last_name, ''),
         a.id;

-- name: ClearDiscipleMaker :exec
-- The disciple maker of [user_id] stays one of their authorities.
UPDATE authorities
SET is_disciple_maker = false
WHERE user_id = $1 AND is_disciple_maker;

-- name: MakeDiscipleMaker :one
-- One of [user_id]'s authorities (not declined) becomes their disciple maker.
UPDATE authorities
SET is_disciple_maker = true
WHERE id = $1 AND user_id = $2 AND status <> 'declined'
RETURNING *;
