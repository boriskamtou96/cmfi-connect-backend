-- name: GetUserByIdentity :one
-- The account a Google or Apple account signs in to.
SELECT u.*
FROM users u
JOIN user_identities i ON i.user_id = u.id
WHERE i.provider = $1 AND i.subject = $2;

-- name: GetUserByEmail :one
-- The account that gave this email (sign-up or profile), without case.
SELECT u.*
FROM users u
LEFT JOIN profiles p ON p.user_id = u.id
WHERE lower(u.email) = lower(@email::text) OR lower(p.email) = lower(@email::text)
ORDER BY u.id
LIMIT 1;

-- name: CreateUserIdentity :exec
INSERT INTO user_identities (provider, subject, user_id, email)
VALUES ($1, $2, $3, $4)
ON CONFLICT (provider, subject) DO NOTHING;

-- name: UserHasIdentity :one
SELECT EXISTS (
    SELECT 1 FROM user_identities
    WHERE user_id = $1 AND provider = $2 AND subject = $3
);

-- name: RegisterSocialUser :one
-- An account made with Google or Apple: its password is a random one nobody
-- knows (has_password false) until the member chooses one.
INSERT INTO users (first_name, last_name, phone_number, email, hash_password, username, has_password)
VALUES ($1, $2, $3, $4, $5, $6, false)
RETURNING *;
