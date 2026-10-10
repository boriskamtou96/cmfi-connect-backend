-- name: CreateMedia :one
INSERT INTO media (key, content_type, width, height, color, size_bytes, uploaded_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListMediaByIDs :many
SELECT *
FROM media
WHERE id = ANY(@ids::bigint[]);

-- name: CreateEventSeries :one
INSERT INTO event_series (title)
VALUES ($1)
RETURNING *;

-- name: CreateEvent :one
INSERT INTO events (
    series_id, title, theme, theme_verse, description, starts_at, ends_at, time_zone,
    venue, address, city, country, maps_url, contact_name, contact_phone, poster_id,
    registration_open, registration_deadline, capacity, asks_lodging, asks_departure_city,
    fee_note, status, created_by
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8,
    $9, $10, $11, $12, $13, $14, $15, $16,
    $17, $18, $19, $20, $21,
    $22, $23, $24
)
RETURNING *;

-- name: UpdateEvent :one
UPDATE events
SET title = $2,
    theme = $3,
    theme_verse = $4,
    description = $5,
    starts_at = $6,
    ends_at = $7,
    time_zone = $8,
    venue = $9,
    address = $10,
    city = $11,
    country = $12,
    maps_url = $13,
    contact_name = $14,
    contact_phone = $15,
    poster_id = $16,
    registration_open = $17,
    registration_deadline = $18,
    capacity = $19,
    asks_lodging = $20,
    asks_departure_city = $21,
    fee_note = $22,
    status = $23,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetEventSeries :exec
UPDATE events
SET series_id = $2
WHERE id = $1;

-- name: GetEvent :one
SELECT *
FROM events
WHERE id = $1;

-- name: LockEvent :one
-- Taken before counting the registrations, so two people can't pass the
-- capacity at the same time.
SELECT *
FROM events
WHERE id = $1
FOR UPDATE;

-- name: DeleteEvent :execrows
DELETE FROM events
WHERE id = $1;

-- name: ListUpcomingEvents :many
-- Not over yet (an event in progress is upcoming), the soonest first.
SELECT *
FROM events
WHERE status <> 'draft' AND ends_at >= @now
ORDER BY starts_at, id
LIMIT @lim OFFSET @off;

-- name: ListPastEvents :many
SELECT *
FROM events
WHERE status <> 'draft' AND ends_at < @now
ORDER BY starts_at DESC, id DESC
LIMIT @lim OFFSET @off;

-- name: ListAllEvents :many
-- For the admins: drafts included, the latest first.
SELECT *
FROM events
ORDER BY starts_at DESC, id DESC
LIMIT @lim OFFSET @off;

-- name: ListEventSeriesEditions :many
SELECT *
FROM events
WHERE series_id = $1 AND status <> 'draft'
ORDER BY starts_at DESC;

-- name: CountEventRegistrations :many
SELECT event_id,
       COUNT(*)::int AS registrations,
       COALESCE(SUM(people), 0)::int AS people
FROM event_registrations
WHERE event_id = ANY(@event_ids::bigint[])
GROUP BY event_id;

-- name: ListUserRegistrations :many
SELECT *
FROM event_registrations
WHERE user_id = @user_id AND event_id = ANY(@event_ids::bigint[]);

-- name: SumOtherRegistrationsPeople :one
SELECT COALESCE(SUM(people), 0)::int
FROM event_registrations
WHERE event_id = $1 AND user_id <> $2;

-- name: UpsertEventRegistration :one
INSERT INTO event_registrations (event_id, user_id, people, needs_lodging, departure_city)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (event_id, user_id) DO UPDATE
SET people = EXCLUDED.people,
    needs_lodging = EXCLUDED.needs_lodging,
    departure_city = EXCLUDED.departure_city,
    updated_at = now()
RETURNING *;

-- name: DeleteEventRegistration :execrows
DELETE FROM event_registrations
WHERE event_id = $1 AND user_id = $2;

-- name: ListEventRegistrations :many
SELECT *
FROM event_registrations
WHERE event_id = $1
ORDER BY created_at;

-- name: ListEventSessions :many
SELECT *
FROM event_sessions
WHERE event_id = $1
ORDER BY starts_at, id;

-- name: DeleteEventSessions :exec
DELETE FROM event_sessions
WHERE event_id = $1;

-- name: CreateEventSession :one
INSERT INTO event_sessions (event_id, starts_at, ends_at, title, speaker, place, description, hymns)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: CreateEventPost :one
INSERT INTO event_posts (event_id, body, image_id, pinned, author_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListEventPosts :many
-- Pinned first, then the latest.
SELECT *
FROM event_posts
WHERE event_id = $1
ORDER BY pinned DESC, created_at DESC, id DESC;

-- name: DeleteEventPost :execrows
DELETE FROM event_posts
WHERE id = $1 AND event_id = $2;

-- name: CreateEventPhoto :one
INSERT INTO event_photos (event_id, media_id, caption)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListEventPhotos :many
SELECT *
FROM event_photos
WHERE event_id = $1
ORDER BY id;

-- name: DeleteEventPhoto :execrows
DELETE FROM event_photos
WHERE id = $1 AND event_id = $2;
