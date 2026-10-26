DROP VIEW IF EXISTS document_details;

DROP TRIGGER IF EXISTS documents_before_save ON documents;
DROP TRIGGER IF EXISTS document_categories_refresh ON document_categories;

DROP FUNCTION IF EXISTS documents_before_save();
DROP FUNCTION IF EXISTS refresh_documents_for_category();
DROP FUNCTION IF EXISTS document_search_text(documents);

DROP INDEX IF EXISTS documents_search_trgm;
DROP INDEX IF EXISTS documents_category_idx;

ALTER TABLE documents
    DROP COLUMN IF EXISTS search_text,
    DROP COLUMN IF EXISTS category_id;

DROP TABLE IF EXISTS document_categories;
