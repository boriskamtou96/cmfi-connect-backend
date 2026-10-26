-- Back to the free-text speakers of 000035.

DROP TRIGGER IF EXISTS teaching_speakers_refresh ON teaching_speakers;
DROP FUNCTION IF EXISTS refresh_teachings_for_speaker();

DROP VIEW teaching_details;

CREATE VIEW teaching_details AS
SELECT t.*,
       m.key AS audio_key,
       m.content_type AS audio_type,
       m.size_bytes AS audio_size,
       c.slug AS category_slug,
       c.name AS category_name,
       e.title AS edition_title,
       e.theme AS edition_theme
FROM teachings t
JOIN media m ON m.id = t.audio_id
LEFT JOIN teaching_categories c ON c.id = t.category_id
LEFT JOIN teaching_editions e ON e.id = t.edition_id;

ALTER TABLE teachings DROP COLUMN speaker_id;

CREATE OR REPLACE FUNCTION teachings_before_save()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.category_id IS NOT NULL THEN
        SELECT name INTO NEW.category FROM teaching_categories WHERE id = NEW.category_id;
    END IF;
    IF NEW.edition_id IS NOT NULL THEN
        SELECT year INTO NEW.year FROM teaching_editions WHERE id = NEW.edition_id;
    ELSIF NEW.year IS NULL AND NEW.recorded_on IS NOT NULL THEN
        NEW.year := EXTRACT(YEAR FROM NEW.recorded_on)::smallint;
    END IF;
    NEW.updated_at := now();
    NEW.search_text := teaching_search_text(NEW);
    RETURN NEW;
END;
$$;

DROP TABLE teaching_speakers;
