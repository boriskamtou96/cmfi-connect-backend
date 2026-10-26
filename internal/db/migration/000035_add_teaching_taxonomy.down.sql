DROP VIEW IF EXISTS teaching_details;

DROP TRIGGER IF EXISTS teachings_before_save ON teachings;
DROP TRIGGER IF EXISTS teaching_categories_refresh ON teaching_categories;
DROP TRIGGER IF EXISTS teaching_editions_refresh ON teaching_editions;

DROP FUNCTION IF EXISTS teachings_before_save();
DROP FUNCTION IF EXISTS refresh_teachings_for_category();
DROP FUNCTION IF EXISTS refresh_teachings_for_edition();
DROP FUNCTION IF EXISTS teaching_search_text(teachings);

DROP INDEX IF EXISTS teachings_search_trgm;
DROP INDEX IF EXISTS teachings_year_idx;
DROP INDEX IF EXISTS teachings_edition_idx;
DROP INDEX IF EXISTS teachings_category_idx;

ALTER TABLE teachings
    DROP COLUMN IF EXISTS search_text,
    DROP COLUMN IF EXISTS position,
    DROP COLUMN IF EXISTS day_number,
    DROP COLUMN IF EXISTS year,
    DROP COLUMN IF EXISTS edition_id,
    DROP COLUMN IF EXISTS category_id;

DROP TABLE IF EXISTS teaching_editions;
DROP TABLE IF EXISTS teaching_categories;
