-- name: ListTeachingsFiltered :many
-- Une page d'enseignements, les publiés d'abord, les messages les plus
-- récents en premier. La recherche, la catégorie, l'orateur, l'année et le
-- jour sont filtrés en base: la pagination et le total restent justes.
SELECT *
FROM teaching_details t
WHERE (sqlc.arg(with_drafts)::boolean OR t.published)
  AND (sqlc.arg(category_id)::bigint = 0 OR t.category_id = sqlc.arg(category_id))
  AND (sqlc.arg(edition_id)::bigint = 0 OR t.edition_id = sqlc.arg(edition_id))
  AND (sqlc.arg(year)::int = 0 OR t.year = sqlc.arg(year)::int)
  AND (sqlc.arg(day)::int = 0 OR t.day_number = sqlc.arg(day)::int)
  AND (sqlc.narg(from_date)::date IS NULL OR t.recorded_on >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR t.recorded_on <= sqlc.narg(to_date)::date)
  AND (sqlc.arg(speaker)::text = '' OR lower(public.unaccent(t.speaker)) LIKE '%' || sqlc.arg(speaker)::text || '%')
  AND (sqlc.arg(speaker_id)::bigint = 0 OR t.speaker_id = sqlc.arg(speaker_id))
  AND (sqlc.arg(search)::text = '' OR NOT EXISTS (
      SELECT 1
      FROM unnest(string_to_array(sqlc.arg(search)::text, ' ')) AS word
      WHERE word <> '' AND t.search_text NOT LIKE '%' || word || '%'
  ))
ORDER BY t.published DESC,
         t.year DESC NULLS LAST,
         t.day_number ASC NULLS LAST,
         t.position ASC,
         t.recorded_on DESC NULLS LAST,
         t.published_at DESC NULLS LAST,
         t.updated_at DESC,
         t.id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountTeachingsFiltered :one
SELECT count(*)
FROM teaching_details t
WHERE (sqlc.arg(with_drafts)::boolean OR t.published)
  AND (sqlc.arg(category_id)::bigint = 0 OR t.category_id = sqlc.arg(category_id))
  AND (sqlc.arg(edition_id)::bigint = 0 OR t.edition_id = sqlc.arg(edition_id))
  AND (sqlc.arg(year)::int = 0 OR t.year = sqlc.arg(year)::int)
  AND (sqlc.arg(day)::int = 0 OR t.day_number = sqlc.arg(day)::int)
  AND (sqlc.narg(from_date)::date IS NULL OR t.recorded_on >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR t.recorded_on <= sqlc.narg(to_date)::date)
  AND (sqlc.arg(speaker)::text = '' OR lower(public.unaccent(t.speaker)) LIKE '%' || sqlc.arg(speaker)::text || '%')
  AND (sqlc.arg(speaker_id)::bigint = 0 OR t.speaker_id = sqlc.arg(speaker_id))
  AND (sqlc.arg(search)::text = '' OR NOT EXISTS (
      SELECT 1
      FROM unnest(string_to_array(sqlc.arg(search)::text, ' ')) AS word
      WHERE word <> '' AND t.search_text NOT LIKE '%' || word || '%'
  ));

-- name: GetTeaching :one
SELECT * FROM teaching_details WHERE id = $1;

-- name: CreateTeaching :one
INSERT INTO teachings (
    title, speaker, speaker_id, description, category, category_id, edition_id, year, day_number, position,
    recorded_on, audio_id, duration_seconds, cover_id, published, published_at, created_by
)
VALUES (
    sqlc.arg(title), sqlc.arg(speaker), sqlc.narg(speaker_id), sqlc.arg(description), sqlc.arg(category),
    sqlc.narg(category_id), sqlc.narg(edition_id), sqlc.narg(year), sqlc.narg(day_number), sqlc.arg(position),
    sqlc.narg(recorded_on), sqlc.arg(audio_id), sqlc.arg(duration_seconds), sqlc.narg(cover_id),
    sqlc.arg(published), CASE WHEN sqlc.arg(published)::boolean THEN now() END, sqlc.narg(created_by)
)
RETURNING id;

-- name: UpdateTeaching :one
-- published_at stays the date of the first publication.
UPDATE teachings
SET title = sqlc.arg(title),
    speaker = sqlc.arg(speaker),
    speaker_id = sqlc.narg(speaker_id),
    description = sqlc.arg(description),
    category = sqlc.arg(category),
    category_id = sqlc.narg(category_id),
    edition_id = sqlc.narg(edition_id),
    year = sqlc.narg(year),
    day_number = sqlc.narg(day_number),
    position = sqlc.arg(position),
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

-- name: ReplaceMediaFile :execrows
-- A sound re-encoded smaller: its new file, under the same media (the
-- teaching that lists it doesn't change). No row once deleted meanwhile.
UPDATE media
SET key = $2, content_type = $3, size_bytes = $4
WHERE id = $1;

-- name: ListTeachingCategories :many
SELECT c.*, count(t.id)::int AS teaching_count
FROM teaching_categories c
LEFT JOIN teachings t ON t.category_id = c.id AND (sqlc.arg(with_drafts)::boolean OR t.published)
WHERE (sqlc.arg(with_archived)::boolean OR c.archived_at IS NULL)
GROUP BY c.id
ORDER BY c.position, lower(c.name);

-- name: GetTeachingCategory :one
SELECT * FROM teaching_categories WHERE id = $1;

-- name: GetTeachingCategoryBySlug :one
SELECT * FROM teaching_categories WHERE slug = $1 AND archived_at IS NULL;

-- name: GetTeachingCategoryBySlugAny :one
SELECT * FROM teaching_categories WHERE slug = $1;

-- name: GetTeachingCategoryByName :one
SELECT * FROM teaching_categories
WHERE lower(btrim(name)) = lower(btrim(sqlc.arg(name))) AND archived_at IS NULL
LIMIT 1;

-- name: CreateTeachingCategory :one
INSERT INTO teaching_categories (name, slug, description, color, icon, parent_id, annual, position)
VALUES (
    sqlc.arg(name), sqlc.arg(slug), sqlc.arg(description), sqlc.arg(color), sqlc.arg(icon),
    sqlc.narg(parent_id), sqlc.arg(annual), sqlc.arg(position)
)
RETURNING *;

-- name: UpdateTeachingCategory :one
UPDATE teaching_categories
SET name = sqlc.arg(name),
    slug = sqlc.arg(slug),
    description = sqlc.arg(description),
    color = sqlc.arg(color),
    icon = sqlc.arg(icon),
    parent_id = sqlc.narg(parent_id),
    annual = sqlc.arg(annual),
    position = sqlc.arg(position),
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ArchiveTeachingCategory :one
UPDATE teaching_categories
SET archived_at = CASE WHEN sqlc.arg(archived)::boolean THEN now() ELSE NULL END,
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CountTeachingsInCategory :one
SELECT count(*) FROM teachings WHERE category_id = $1;

-- name: ListTeachingEditions :many
SELECT e.*, c.name AS category_name, c.slug AS category_slug, count(t.id)::int AS teaching_count
FROM teaching_editions e
JOIN teaching_categories c ON c.id = e.category_id
LEFT JOIN teachings t ON t.edition_id = e.id AND (sqlc.arg(with_drafts)::boolean OR t.published)
GROUP BY e.id, c.name, c.slug, c.position
ORDER BY e.year DESC, c.position, lower(c.name);

-- name: GetTeachingEdition :one
SELECT * FROM teaching_editions WHERE id = $1;

-- name: CreateTeachingEdition :one
INSERT INTO teaching_editions (category_id, year, title, theme, starts_on, ends_on, venue, cover_id)
VALUES (
    sqlc.arg(category_id), sqlc.arg(year), sqlc.arg(title), sqlc.arg(theme),
    sqlc.narg(starts_on), sqlc.narg(ends_on), sqlc.arg(venue), sqlc.narg(cover_id)
)
RETURNING *;

-- name: UpdateTeachingEdition :one
UPDATE teaching_editions
SET category_id = sqlc.arg(category_id),
    year = sqlc.arg(year),
    title = sqlc.arg(title),
    theme = sqlc.arg(theme),
    starts_on = sqlc.narg(starts_on),
    ends_on = sqlc.narg(ends_on),
    venue = sqlc.arg(venue),
    cover_id = sqlc.narg(cover_id),
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteTeachingEdition :one
DELETE FROM teaching_editions WHERE id = $1 RETURNING id;

-- name: CountTeachingsInEdition :one
SELECT count(*) FROM teachings WHERE edition_id = $1;

-- name: ListTeachingCategoryCounts :many
SELECT c.id, c.name, c.slug, count(t.id)::int AS count
FROM teaching_categories c
JOIN teachings t ON t.category_id = c.id AND (sqlc.arg(with_drafts)::boolean OR t.published)
WHERE c.archived_at IS NULL
GROUP BY c.id
ORDER BY c.position, lower(c.name);

-- name: ListTeachingSpeakerFacets :many
-- The speakers the published teachings name, for the filter of the apps.
SELECT s.id, s.name, s.slug, count(t.id)::int AS count
FROM teaching_speakers s
JOIN teachings t ON t.speaker_id = s.id AND (sqlc.arg(with_drafts)::boolean OR t.published)
WHERE s.archived_at IS NULL
GROUP BY s.id
ORDER BY lower(s.name);

-- name: ListTeachingSpeakers :many
-- The admins' speakers, archived ones too, with how many teachings each.
SELECT s.*, count(t.id)::int AS teaching_count
FROM teaching_speakers s
LEFT JOIN teachings t ON t.speaker_id = s.id AND (sqlc.arg(with_drafts)::boolean OR t.published)
WHERE (sqlc.arg(with_archived)::boolean OR s.archived_at IS NULL)
GROUP BY s.id
ORDER BY lower(s.name);

-- name: GetTeachingSpeaker :one
SELECT * FROM teaching_speakers WHERE id = $1;

-- name: GetTeachingSpeakerByName :one
-- An active speaker, accents and case left aside.
SELECT * FROM teaching_speakers
WHERE lower(public.unaccent(btrim(name))) = lower(public.unaccent(btrim(sqlc.arg(name))))
  AND archived_at IS NULL
LIMIT 1;

-- name: GetTeachingSpeakerBySlug :one
SELECT * FROM teaching_speakers WHERE slug = $1;

-- name: CreateTeachingSpeaker :one
INSERT INTO teaching_speakers (name, slug, role, bio, photo_id)
VALUES (
    sqlc.arg(name), sqlc.arg(slug), sqlc.arg(role), sqlc.arg(bio), sqlc.narg(photo_id)
)
RETURNING *;

-- name: UpdateTeachingSpeaker :one
UPDATE teaching_speakers
SET name = sqlc.arg(name),
    slug = sqlc.arg(slug),
    role = sqlc.arg(role),
    bio = sqlc.arg(bio),
    photo_id = sqlc.narg(photo_id),
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ArchiveTeachingSpeaker :one
UPDATE teaching_speakers
SET archived_at = CASE WHEN sqlc.arg(archived)::boolean THEN now() ELSE NULL END,
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CountTeachingsOfSpeaker :one
SELECT count(*) FROM teachings WHERE speaker_id = $1;

-- name: DeleteTeachingSpeaker :exec
DELETE FROM teaching_speakers WHERE id = $1;

-- name: MergeTeachingSpeakers :exec
-- The teachings of the merged speakers go to [into_id]; the others are
-- archived. The name of each teaching follows through its before-save trigger.
WITH moved AS (
    UPDATE teachings
    SET speaker_id = sqlc.arg(into_id)::bigint
    WHERE speaker_id = ANY(sqlc.arg(from_ids)::bigint[])
)
UPDATE teaching_speakers
SET archived_at = now(), updated_at = now()
WHERE id = ANY(sqlc.arg(from_ids)::bigint[]) AND id <> sqlc.arg(into_id)::bigint;

-- name: ListTeachingYears :many
SELECT year, count(*)::int AS count
FROM teachings
WHERE year IS NOT NULL AND (sqlc.arg(with_drafts)::boolean OR published)
GROUP BY year
ORDER BY year DESC;

-- name: ListTeachingDays :many
SELECT day_number, count(*)::int AS count
FROM teachings
WHERE edition_id = sqlc.arg(edition_id)
  AND day_number IS NOT NULL
  AND (sqlc.arg(with_drafts)::boolean OR published)
GROUP BY day_number
ORDER BY day_number;
