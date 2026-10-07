-- name: RegisterUser :one
INSERT INTO users(
                  first_name,
                  last_name,
                  phone_number,
                  email,
                  hash_password
)
VALUES ($1, $2, $3, $4, $5)
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
    hash_password = COALESCE($6, hash_password),
WHERE id = $1
RETURNING *;