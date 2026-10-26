-- Nos documents: des catégories stables (Prières, Traités, Prophéties,
-- Réveil…), créées et renommées par les admins. Une catégorie archivée garde
-- ses documents mais quitte les formulaires.

CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE document_categories (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL CHECK (btrim(name) <> ''),
    slug        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    color       TEXT NOT NULL DEFAULT '',
    icon        TEXT NOT NULL DEFAULT '',
    parent_id   BIGINT REFERENCES document_categories(id) ON DELETE SET NULL,
    position    INT NOT NULL DEFAULT 0,
    archived_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX document_categories_name_active
    ON document_categories (lower(btrim(name)))
    WHERE archived_at IS NULL;

CREATE INDEX document_categories_parent_idx ON document_categories (parent_id);

ALTER TABLE documents
    ADD COLUMN category_id BIGINT REFERENCES document_categories(id) ON DELETE SET NULL,
    -- title, description, category and file name, without accents
    ADD COLUMN search_text TEXT NOT NULL DEFAULT '';

CREATE INDEX documents_category_idx ON documents (category_id) WHERE category_id IS NOT NULL;
CREATE INDEX documents_search_trgm ON documents USING gin (search_text gin_trgm_ops);

CREATE OR REPLACE FUNCTION document_search_text(d documents)
RETURNS text
LANGUAGE sql
STABLE
AS $$
    SELECT lower(public.unaccent(concat_ws(' ',
        d.title,
        d.description,
        d.category,
        d.file_name,
        (SELECT c.name FROM document_categories c WHERE c.id = d.category_id)
    )));
$$;

CREATE OR REPLACE FUNCTION documents_before_save()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.category_id IS NOT NULL THEN
        SELECT name INTO NEW.category FROM document_categories WHERE id = NEW.category_id;
    END IF;
    NEW.updated_at := now();
    NEW.search_text := document_search_text(NEW);
    RETURN NEW;
END;
$$;

CREATE TRIGGER documents_before_save
BEFORE INSERT OR UPDATE ON documents
FOR EACH ROW EXECUTE FUNCTION documents_before_save();

CREATE OR REPLACE FUNCTION refresh_documents_for_category()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE documents
    SET category = NEW.name,
        search_text = document_search_text(documents)
    WHERE category_id = NEW.id;
    RETURN NEW;
END;
$$;

CREATE TRIGGER document_categories_refresh
AFTER UPDATE OF name ON document_categories
FOR EACH ROW EXECUTE FUNCTION refresh_documents_for_category();

CREATE VIEW document_details AS
SELECT d.*,
       m.key AS file_key,
       m.content_type AS file_type,
       m.size_bytes AS file_size,
       c.slug AS category_slug,
       c.name AS category_name
FROM documents d
JOIN media m ON m.id = d.file_id
LEFT JOIN document_categories c ON c.id = d.category_id;

INSERT INTO document_categories (name, slug, position) VALUES
    ('Prières', 'prieres', 10),
    ('Traités', 'traites', 20),
    ('Prophéties', 'propheties', 30),
    ('Réveil', 'reveil', 40),
    ('Enseignements', 'enseignements', 50),
    ('Formulaires', 'formulaires', 60),
    ('Règlements', 'reglements', 70),
    ('Autres', 'autres', 100)
ON CONFLICT (slug) DO NOTHING;

WITH names AS (
    SELECT DISTINCT btrim(category) AS name
    FROM documents
    WHERE btrim(category) <> ''
),
prepared AS (
    SELECT name,
           COALESCE(
               NULLIF(trim(both '-' FROM lower(regexp_replace(public.unaccent(name), '[^a-zA-Z0-9]+', '-', 'g'))), ''),
               'categorie-' || md5(name)
           ) AS base
    FROM names
)
INSERT INTO document_categories (name, slug, position)
SELECT name,
       base || CASE
           WHEN row_number() OVER (PARTITION BY base ORDER BY name) = 1 THEN ''
           ELSE '-' || row_number() OVER (PARTITION BY base ORDER BY name)
       END,
       100
FROM prepared
ON CONFLICT (slug) DO NOTHING;

UPDATE documents d
SET category_id = c.id
FROM document_categories c
WHERE d.category_id IS NULL
  AND lower(btrim(d.category)) = lower(btrim(c.name));

UPDATE documents SET search_text = document_search_text(documents);
