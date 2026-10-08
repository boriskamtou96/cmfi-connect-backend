-- name: GetProfile :one
SELECT * FROM profiles
WHERE user_id = $1 LIMIT 1;

-- name: ListProfiles :many
SELECT * FROM profiles
ORDER BY id DESC;

-- name: CreateProfile :one
INSERT INTO profiles (user_id, first_name, last_name, phone_number, email, birth_date, city, country, church, assembly)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpdateProfile :one
UPDATE profiles
SET birth_date = COALESCE($2, birth_date),
    first_name = COALESCE($3, first_name),
    last_name = COALESCE($4, last_name),
    phone_number = COALESCE($5, phone_number),
    email = COALESCE($6, email),
    city = COALESCE($7, city),
    country = COALESCE($8, country),
    church = COALESCE($9, church),
    assembly = COALESCE($10, assembly),
    updated_at = NOW()
WHERE user_id = $1
RETURNING *;