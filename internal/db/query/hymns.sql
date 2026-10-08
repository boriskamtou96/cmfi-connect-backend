-- name: GetHymnBookByCode :one
SELECT * FROM hymn_books
WHERE code = $1
LIMIT 1;

-- name: CreateHymnBook :one
INSERT INTO hymn_books (code, title, language, position)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateHymnBook :execrows
-- Only writes when something differs, so the caller knows if the book changed.
UPDATE hymn_books
SET title = sqlc.arg(title),
    language = sqlc.arg(language),
    position = sqlc.arg(position)
WHERE id = sqlc.arg(id)
  AND (title <> sqlc.arg(title) OR language <> sqlc.arg(language) OR position <> sqlc.arg(position));

-- name: TouchHymnBook :exec
-- A new version of the book: the apps download it again.
UPDATE hymn_books
SET updated_at = NOW()
WHERE id = $1;

-- name: DeleteHymnBook :exec
DELETE FROM hymn_books
WHERE id = $1;

-- name: ListHymnBooks :many
SELECT b.id, b.code, b.title, b.language, b.position, b.updated_at,
       COUNT(h.id)::bigint AS hymn_count
FROM hymn_books b
LEFT JOIN hymns h ON h.book_id = b.id
GROUP BY b.id
ORDER BY b.position, b.title;

-- name: ListHymnNumbers :many
SELECT number FROM hymns
WHERE book_id = $1;

-- name: UpsertHymn :execrows
-- 1 row when the hymn is new or changed, 0 when it is already identical.
INSERT INTO hymns (book_id, number, title, author, parts)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (book_id, number) DO UPDATE
SET title = EXCLUDED.title,
    author = EXCLUDED.author,
    parts = EXCLUDED.parts,
    updated_at = NOW()
WHERE hymns.title IS DISTINCT FROM EXCLUDED.title
   OR hymns.author IS DISTINCT FROM EXCLUDED.author
   OR hymns.parts IS DISTINCT FROM EXCLUDED.parts;

-- name: DeleteHymnsNotIn :execrows
DELETE FROM hymns
WHERE book_id = sqlc.arg(book_id)
  AND NOT (number = ANY(sqlc.arg(numbers)::int[]));

-- name: ListBookHymns :many
SELECT number, title, author, parts FROM hymns
WHERE book_id = $1
ORDER BY number;

-- name: GetHymn :one
SELECT number, title, author, parts FROM hymns
WHERE book_id = $1 AND number = $2
LIMIT 1;
