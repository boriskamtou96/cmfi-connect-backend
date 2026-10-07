-- name: CreateUserAuthority :one
INSERT INTO authorities (first_name, last_name, phone_number, email, is_disciple_maker, user_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetUserAuthorities :many
SELECT *
FROM authorities
ORDER BY id DESC
LIMIT $1
OFFSET $2;

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