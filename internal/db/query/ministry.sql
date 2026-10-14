-- ----- our ministry: the nations -----

-- name: ListNations :many
SELECT *
FROM ministry_nations
ORDER BY position;

-- name: GetNation :one
SELECT *
FROM ministry_nations
WHERE id = $1;

-- name: CountNations :one
SELECT count(*)::int
FROM ministry_nations;

-- name: CreateNation :one
INSERT INTO ministry_nations (position, name, country_code, latitude, longitude, arrival_year)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateNation :one
UPDATE ministry_nations
SET position = $2,
    name = $3,
    country_code = $4,
    latitude = $5,
    longitude = $6,
    arrival_year = $7,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteNation :exec
DELETE FROM ministry_nations
WHERE id = $1;

-- name: ShiftNations :exec
-- Moves by [delta] the nations numbered from [first] to [last] (included):
-- room for one, or the gap of one gone.
UPDATE ministry_nations
SET position = position + @delta::int
WHERE position BETWEEN @first::int AND @last::int;
