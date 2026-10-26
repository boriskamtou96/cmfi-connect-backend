-- name: ListDocumentsFiltered :many
-- Une page de documents, les publiés d'abord, les plus récents en premier.
-- La recherche et la catégorie sont filtrées en base.
SELECT *
FROM document_details d
WHERE (sqlc.arg(with_drafts)::boolean OR d.published)
  AND (sqlc.arg(category_id)::bigint = 0 OR d.category_id = sqlc.arg(category_id))
  AND (sqlc.arg(search)::text = '' OR NOT EXISTS (
      SELECT 1
      FROM unnest(string_to_array(sqlc.arg(search)::text, ' ')) AS word
      WHERE word <> '' AND d.search_text NOT LIKE '%' || word || '%'
  ))
ORDER BY d.published DESC, d.published_at DESC NULLS LAST, d.updated_at DESC, d.id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountDocumentsFiltered :one
SELECT count(*)
FROM document_details d
WHERE (sqlc.arg(with_drafts)::boolean OR d.published)
  AND (sqlc.arg(category_id)::bigint = 0 OR d.category_id = sqlc.arg(category_id))
  AND (sqlc.arg(search)::text = '' OR NOT EXISTS (
      SELECT 1
      FROM unnest(string_to_array(sqlc.arg(search)::text, ' ')) AS word
      WHERE word <> '' AND d.search_text NOT LIKE '%' || word || '%'
  ));

-- name: GetDocument :one
SELECT * FROM document_details WHERE id = $1;

-- name: CreateDocument :one
INSERT INTO documents (title, description, category, category_id, file_id, file_name, published, published_at, created_by)
VALUES (
    sqlc.arg(title), sqlc.arg(description), sqlc.arg(category), sqlc.narg(category_id),
    sqlc.arg(file_id), sqlc.arg(file_name),
    sqlc.arg(published), CASE WHEN sqlc.arg(published)::boolean THEN now() END, sqlc.narg(created_by)
)
RETURNING id;

-- name: UpdateDocument :one
-- published_at stays the date of the first publication.
UPDATE documents
SET title = sqlc.arg(title),
    description = sqlc.arg(description),
    category = sqlc.arg(category),
    category_id = sqlc.narg(category_id),
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

-- name: ListDocumentCategories :many
SELECT c.*, count(d.id)::int AS document_count
FROM document_categories c
LEFT JOIN documents d ON d.category_id = c.id AND (sqlc.arg(with_drafts)::boolean OR d.published)
WHERE (sqlc.arg(with_archived)::boolean OR c.archived_at IS NULL)
GROUP BY c.id
ORDER BY c.position, lower(c.name);

-- name: GetDocumentCategory :one
SELECT * FROM document_categories WHERE id = $1;

-- name: GetDocumentCategoryBySlug :one
SELECT * FROM document_categories WHERE slug = $1 AND archived_at IS NULL;

-- name: GetDocumentCategoryBySlugAny :one
SELECT * FROM document_categories WHERE slug = $1;

-- name: GetDocumentCategoryByName :one
SELECT * FROM document_categories
WHERE lower(btrim(name)) = lower(btrim(sqlc.arg(name))) AND archived_at IS NULL
LIMIT 1;

-- name: CreateDocumentCategory :one
INSERT INTO document_categories (name, slug, description, color, icon, parent_id, position)
VALUES (
    sqlc.arg(name), sqlc.arg(slug), sqlc.arg(description), sqlc.arg(color), sqlc.arg(icon),
    sqlc.narg(parent_id), sqlc.arg(position)
)
RETURNING *;

-- name: UpdateDocumentCategory :one
UPDATE document_categories
SET name = sqlc.arg(name),
    slug = sqlc.arg(slug),
    description = sqlc.arg(description),
    color = sqlc.arg(color),
    icon = sqlc.arg(icon),
    parent_id = sqlc.narg(parent_id),
    position = sqlc.arg(position),
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ArchiveDocumentCategory :one
UPDATE document_categories
SET archived_at = CASE WHEN sqlc.arg(archived)::boolean THEN now() ELSE NULL END,
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CountDocumentsInCategory :one
SELECT count(*) FROM documents WHERE category_id = $1;

-- name: ListDocumentCategoryCounts :many
SELECT c.id, c.name, c.slug, count(d.id)::int AS count
FROM document_categories c
JOIN documents d ON d.category_id = c.id AND (sqlc.arg(with_drafts)::boolean OR d.published)
WHERE c.archived_at IS NULL
GROUP BY c.id
ORDER BY c.position, lower(c.name);
