-- name: RegisterUser :one
INSERT INTO users(
                  first_name,
                  last_name,
                  phone_number,
                  email,
                  hash_password,
                  username
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetUserById :one
SELECT *
FROM users
WHERE id = $1 LIMIT 1;

-- name: GetByPhoneNumber :one
SELECT *
FROM users
WHERE phone_number = $1 LIMIT 1;

-- name: ListUsers :many
SELECT *
FROM users
ORDER BY id DESC
LIMIT $1
OFFSET $2;

-- name: DeleteUser :exec
DELETE FROM users
WHERE id = $1;

-- name: UpdateUser :one
UPDATE users
SET
    first_name = COALESCE($2, first_name),
    last_name = COALESCE($3, last_name),
    phone_number = COALESCE($4, phone_number),
    email = COALESCE($5, email),
    hash_password = COALESCE($6, hash_password)
WHERE id = $1
RETURNING *;

-- name: GetUserByUsername :one
SELECT *
FROM users
WHERE username = $1 LIMIT 1;

-- name: ListTakenUsernames :many
-- Among the candidates, the pseudos already used.
SELECT username
FROM users
WHERE username = ANY(@candidates::text[]);

-- name: UpdateUsername :one
UPDATE users
SET username = $2
WHERE id = $1
RETURNING *;

-- name: GetUserRole :one
SELECT role
FROM users
WHERE id = $1;

-- name: UpdateUserRole :one
UPDATE users
SET role = $2
WHERE id = $1
RETURNING *;

-- name: ListUsersByRoles :many
SELECT *
FROM users
WHERE role = ANY(@roles::text[])
ORDER BY role DESC, username;

-- name: ListMemberCards :many
-- What members show of themselves, the profile first (the user row has the
-- names of the sign-up). Contact fields are for the handlers to give or hide.
SELECT u.id,
       u.username,
       COALESCE(NULLIF(p.first_name, ''), u.first_name)::text AS first_name,
       COALESCE(NULLIF(p.last_name, ''), u.last_name, '')::text AS last_name,
       COALESCE(NULLIF(p.phone_number, ''), u.phone_number)::text AS phone_number,
       COALESCE(NULLIF(p.email, ''), u.email, '')::text AS email,
       COALESCE(p.church, '')::text AS church,
       COALESCE(p.assembly, '')::text AS assembly,
       -- the profile photo, avatar_id 0 without one
       COALESCE(m.id, 0)::bigint AS avatar_id,
       COALESCE(m.key, '')::text AS avatar_key,
       COALESCE(m.width, 0)::int AS avatar_width,
       COALESCE(m.height, 0)::int AS avatar_height,
       COALESCE(m.color, '')::text AS avatar_color
FROM users u
LEFT JOIN profiles p ON p.user_id = u.id
LEFT JOIN media m ON m.id = u.avatar_media_id
WHERE u.id = ANY(@ids::bigint[]);

-- name: CountSuperadmins :one
SELECT count(*)
FROM users
WHERE role = 'superadmin';

-- name: DeleteAuthoritiesLinkedTo :exec
-- The authorities of other members that point to this member: their copy
-- of the name and contact goes with the account.
DELETE FROM authorities
WHERE linked_user_id = $1;

-- name: DeleteUserReports :exec
-- Before the account: its personal items can't go while entries use them.
DELETE FROM daily_reports
WHERE user_id = $1;

-- name: UpdateUserPassword :exec
UPDATE users
SET hash_password = $2
WHERE id = $1;

-- name: ListAdminMembers :many
-- Every account as the back office shows it (no spiritual data): the
-- profile first, the last time the app was opened on a phone, how many
-- phones get the push and the profile photo. The newest first.
SELECT u.id,
       u.username,
       u.role,
       u.created_at,
       COALESCE(NULLIF(p.first_name, ''), u.first_name)::text AS first_name,
       COALESCE(NULLIF(p.last_name, ''), u.last_name, '')::text AS last_name,
       COALESCE(NULLIF(p.phone_number, ''), u.phone_number)::text AS phone_number,
       COALESCE(NULLIF(p.email, ''), u.email, '')::text AS email,
       COALESCE(p.country, '')::text AS country,
       COALESCE(p.city, '')::text AS city,
       COALESCE(p.church, '')::text AS church,
       COALESCE(p.assembly, '')::text AS assembly,
       dv.last_seen_at,
       dv.phones,
       COALESCE(m.id, 0)::bigint AS avatar_id,
       COALESCE(m.key, '')::text AS avatar_key,
       COALESCE(m.width, 0)::int AS avatar_width,
       COALESCE(m.height, 0)::int AS avatar_height,
       COALESCE(m.color, '')::text AS avatar_color
FROM users u
LEFT JOIN profiles p ON p.user_id = u.id
LEFT JOIN media m ON m.id = u.avatar_media_id
LEFT JOIN LATERAL (
    SELECT max(d.updated_at) AS last_seen_at, count(*)::int AS phones
    FROM devices d
    WHERE d.user_id = u.id
) dv ON TRUE
ORDER BY u.created_at DESC, u.id DESC;

-- name: GetAdminMember :one
-- One account as the back office shows it (see ListAdminMembers).
SELECT u.id,
       u.username,
       u.role,
       u.created_at,
       COALESCE(NULLIF(p.first_name, ''), u.first_name)::text AS first_name,
       COALESCE(NULLIF(p.last_name, ''), u.last_name, '')::text AS last_name,
       COALESCE(NULLIF(p.phone_number, ''), u.phone_number)::text AS phone_number,
       COALESCE(NULLIF(p.email, ''), u.email, '')::text AS email,
       COALESCE(p.country, '')::text AS country,
       COALESCE(p.city, '')::text AS city,
       COALESCE(p.church, '')::text AS church,
       COALESCE(p.assembly, '')::text AS assembly,
       dv.last_seen_at,
       dv.phones,
       COALESCE(m.id, 0)::bigint AS avatar_id,
       COALESCE(m.key, '')::text AS avatar_key,
       COALESCE(m.width, 0)::int AS avatar_width,
       COALESCE(m.height, 0)::int AS avatar_height,
       COALESCE(m.color, '')::text AS avatar_color
FROM users u
LEFT JOIN profiles p ON p.user_id = u.id
LEFT JOIN media m ON m.id = u.avatar_media_id
LEFT JOIN LATERAL (
    SELECT max(d.updated_at) AS last_seen_at, count(*)::int AS phones
    FROM devices d
    WHERE d.user_id = u.id
) dv ON TRUE
WHERE u.id = @id;

-- name: GetUserAvatar :one
-- The profile photo of a member (no row without one).
SELECT m.*
FROM users u
JOIN media m ON m.id = u.avatar_media_id
WHERE u.id = $1;

-- name: SetUserAvatar :one
-- Puts a profile photo (NULL takes it off) and gives the one it replaces,
-- for its file to go. No row when the member doesn't exist.
UPDATE users u
SET avatar_media_id = @avatar_media_id
FROM (
    SELECT o.id, o.avatar_media_id
    FROM users o
    WHERE o.id = @id
    FOR UPDATE
) previous
WHERE u.id = previous.id
RETURNING previous.avatar_media_id AS previous_media_id;
