-- name: ListTeachings :many
-- Every teaching with its sound, the published ones first, the newest
-- message first (then the newest sent), then the drafts; the API filters
-- (category, speaker, search) and pages them.
SELECT t.*, m.key AS audio_key, m.content_type AS audio_type, m.size_bytes AS audio_size
FROM teachings t
JOIN media m ON m.id = t.audio_id
WHERE sqlc.arg(with_drafts)::boolean OR t.published
ORDER BY t.published DESC, t.recorded_on DESC NULLS LAST, t.published_at DESC NULLS LAST, t.updated_at DESC, t.id DESC;

-- name: GetTeaching :one
SELECT t.*, m.key AS audio_key, m.content_type AS audio_type, m.size_bytes AS audio_size
FROM teachings t
JOIN media m ON m.id = t.audio_id
WHERE t.id = $1;

-- name: CreateTeaching :one
INSERT INTO teachings (
    title, speaker, description, category, recorded_on, audio_id, duration_seconds, cover_id,
    published, published_at, created_by
)
VALUES (
    sqlc.arg(title), sqlc.arg(speaker), sqlc.arg(description), sqlc.arg(category), sqlc.narg(recorded_on),
    sqlc.arg(audio_id), sqlc.arg(duration_seconds), sqlc.narg(cover_id),
    sqlc.arg(published), CASE WHEN sqlc.arg(published)::boolean THEN now() END, sqlc.narg(created_by)
)
RETURNING id;

-- name: UpdateTeaching :one
-- published_at stays the date of the first publication.
UPDATE teachings
SET title = sqlc.arg(title),
    speaker = sqlc.arg(speaker),
    description = sqlc.arg(description),
    category = sqlc.arg(category),
    recorded_on = sqlc.narg(recorded_on),
    audio_id = sqlc.arg(audio_id),
    duration_seconds = sqlc.arg(duration_seconds),
    cover_id = sqlc.narg(cover_id),
    published = sqlc.arg(published),
    published_at = CASE WHEN sqlc.arg(published)::boolean THEN COALESCE(published_at, now()) ELSE published_at END,
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING id;

-- name: DeleteTeaching :one
DELETE FROM teachings WHERE id = $1
RETURNING audio_id, cover_id;

-- name: CountTeachingsWithMedia :one
-- A sound or a cover still used by another teaching is kept.
SELECT count(*) FROM teachings WHERE audio_id = sqlc.arg(media_id) OR cover_id = sqlc.arg(media_id);
