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
       COALESCE(p.assembly, '')::text AS assembly
FROM users u
LEFT JOIN profiles p ON p.user_id = u.id
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
