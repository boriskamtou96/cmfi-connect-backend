-- name: GetProfile :one
SELECT * FROM profiles
WHERE user_id = $1 LIMIT 1;

-- name: ListProfiles :many
SELECT * FROM profiles
ORDER BY id DESC;

-- name: CreateProfile :one
INSERT INTO profiles (user_id, birth_date, city, country, church, assembly)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateProfile :one
UPDATE profiles
SET birth_date = COALESCE($2, birth_date),
    city = COALESCE($3, city),
    country = COALESCE($4, country),
    church = COALESCE($5, church),
    assembly = COALESCE($6, assembly),
    updated_at = NOW()
WHERE user_id = $1
RETURNING *;