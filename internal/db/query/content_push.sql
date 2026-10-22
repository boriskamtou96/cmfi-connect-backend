-- name: ClaimTeachingNotification :one
-- Marks a published teaching as announced: no row when it is a draft or
-- was announced already (two clicks at once: only one gets it).
UPDATE teachings
SET notified_at = now()
WHERE id = $1 AND published AND notified_at IS NULL
RETURNING title, speaker, cover_id;

-- name: ClaimDocumentNotification :one
UPDATE documents
SET notified_at = now()
WHERE id = $1 AND published AND notified_at IS NULL
RETURNING title;

-- name: ClaimEventNotification :one
-- A cancelled event is not announced.
UPDATE events
SET notified_at = now()
WHERE id = $1 AND status = 'published' AND notified_at IS NULL
RETURNING title, starts_at, time_zone, city, poster_id;

-- name: ClaimPrayerTopicNotification :one
UPDATE prayer_topics
SET notified_at = now()
WHERE id = $1 AND status = 'published' AND notified_at IS NULL
RETURNING title;

-- name: ListAllDevices :many
-- Every phone of the members, with its language.
SELECT token, language
FROM devices
ORDER BY id;
