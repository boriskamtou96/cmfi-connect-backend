-- name: ListDocuments :many
-- Every document with its file, the published ones first by date, then the
-- drafts; the API filters (category, search) and pages them.
SELECT d.*, m.key AS file_key, m.content_type AS file_type, m.size_bytes AS file_size
FROM documents d
JOIN media m ON m.id = d.file_id
WHERE sqlc.arg(with_drafts)::boolean OR d.published
ORDER BY d.published DESC, d.published_at DESC NULLS LAST, d.updated_at DESC, d.id DESC;

-- name: GetDocument :one
SELECT d.*, m.key AS file_key, m.content_type AS file_type, m.size_bytes AS file_size
FROM documents d
JOIN media m ON m.id = d.file_id
WHERE d.id = $1;

-- name: CreateDocument :one
INSERT INTO documents (title, description, category, file_id, file_name, published, published_at, created_by)
VALUES (
    sqlc.arg(title), sqlc.arg(description), sqlc.arg(category), sqlc.arg(file_id), sqlc.arg(file_name),
    sqlc.arg(published), CASE WHEN sqlc.arg(published)::boolean THEN now() END, sqlc.narg(created_by)
)
RETURNING id;

-- name: UpdateDocument :one
-- published_at stays the date of the first publication.
UPDATE documents
SET title = sqlc.arg(title),
    description = sqlc.arg(description),
    category = sqlc.arg(category),
    file_id = sqlc.arg(file_id),
    file_name = sqlc.arg(file_name),
    published = sqlc.arg(published),
    published_at = CASE WHEN sqlc.arg(published)::boolean THEN COALESCE(published_at, now()) ELSE published_at END,
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING id;

-- name: DeleteDocument :one
DELETE FROM documents WHERE id = $1
RETURNING file_id;

-- name: CountDocumentsWithFile :one
-- A file still listed by another document is kept.
SELECT count(*) FROM documents WHERE file_id = $1;
