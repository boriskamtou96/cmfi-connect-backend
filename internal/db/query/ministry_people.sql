-- name: ListMinistryPeople :many
-- The people of [kind] in their order; the drafts only for the admins.
SELECT * FROM ministry_people
WHERE kind = sqlc.arg(kind)
  AND (published OR sqlc.arg(with_drafts)::bool)
ORDER BY position, id;

-- name: GetMinistryPerson :one
SELECT * FROM ministry_people
WHERE id = $1;

-- name: CreateMinistryPerson :one
-- After the others of the same kind.
INSERT INTO ministry_people (kind, name, photo_id, country_code, role, since_year, years, story, published, position)
VALUES (
    sqlc.arg(kind),
    sqlc.arg(name),
    sqlc.narg(photo_id),
    sqlc.arg(country_code),
    sqlc.arg(role),
    sqlc.narg(since_year),
    sqlc.arg(years),
    sqlc.arg(story),
    sqlc.arg(published),
    (SELECT COALESCE(MAX(position), 0) + 1 FROM ministry_people WHERE kind = sqlc.arg(kind))
)
RETURNING *;

-- name: UpdateMinistryPerson :one
UPDATE ministry_people
SET name = sqlc.arg(name),
    photo_id = sqlc.narg(photo_id),
    country_code = sqlc.arg(country_code),
    role = sqlc.arg(role),
    since_year = sqlc.narg(since_year),
    years = sqlc.arg(years),
    story = sqlc.arg(story),
    published = sqlc.arg(published),
    position = COALESCE(sqlc.narg(position), position),
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteMinistryPerson :one
DELETE FROM ministry_people
WHERE id = $1
RETURNING photo_id;
