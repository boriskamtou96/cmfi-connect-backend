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
-- Whether [user_id] shows their regularity to this authority (their disciple maker).
UPDATE authorities
SET shares_regularity = $3
WHERE id = $1 AND user_id = $2
RETURNING *;

-- name: ListDisciples :many
-- The members who chose [maker_id] as their disciple maker and whose link they
-- accepted, by name (the profile's first, like the member cards).
SELECT a.id, a.user_id, a.shares_regularity
FROM authorities a
JOIN users u ON u.id = a.user_id
LEFT JOIN profiles p ON p.user_id = u.id
WHERE a.linked_user_id = @maker_id::bigint
  AND a.is_disciple_maker
  AND a.status = 'accepted'
ORDER BY COALESCE(NULLIF(p.first_name, ''), u.first_name),
         COALESCE(NULLIF(p.last_name, ''), u.last_name, ''),
         a.id;
