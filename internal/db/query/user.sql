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
    first_name = $2,
    last_name = $3,
    phone_number = $4,
    email = $5,
    hash_password = $6
WHERE id = $1
RETURNING *;