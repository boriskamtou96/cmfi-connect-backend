-- name: RegisterUser :one
INSERT INTO users(
                  first_name,
                  last_name,
                  hash_password
)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetUserById :one
SELECT *
FROM users
WHERE id = $1 LIMIT 1;

-- name: ListUsers :many
SELECT *
FROM users
ORDER BY id
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
    hash_password = $4
WHERE id = $1
RETURNING *;