-- Les orateurs des enseignements: une fiche par personne (nom, rôle, bio,
-- photo), créée et renommée par les admins. Le texte [teachings.speaker] reste
-- pour les apps déjà installées: il suit la fiche, et la recherche aussi.

CREATE TABLE teaching_speakers (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL CHECK (btrim(name) <> ''),
    -- "theodore-andoseh": the stable name in the addresses and the API
    slug        TEXT NOT NULL UNIQUE,
    -- "Pasteur", "Serviteur de Dieu"…
    role        TEXT NOT NULL DEFAULT '',
    bio         TEXT NOT NULL DEFAULT '',
    photo_id    BIGINT REFERENCES media(id) ON DELETE SET NULL,
    -- an archived speaker keeps their teachings and leaves the forms
    archived_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One active speaker per name, case left aside (the API also checks accents
-- on write; unaccent is not IMMUTABLE, so it can't index).
CREATE UNIQUE INDEX teaching_speakers_name_active
    ON teaching_speakers (lower(btrim(name)))
    WHERE archived_at IS NULL;

ALTER TABLE teachings
    ADD COLUMN speaker_id BIGINT REFERENCES teaching_speakers(id) ON DELETE SET NULL;

CREATE INDEX teachings_speaker_idx ON teachings (speaker_id) WHERE speaker_id IS NOT NULL;

-- The speakers the community already knows, one per free-text name.
WITH names AS (
    SELECT DISTINCT btrim(speaker) AS name
    FROM teachings
    WHERE btrim(speaker) <> ''
),
prepared AS (
    SELECT name,
           COALESCE(
               NULLIF(trim(both '-' FROM lower(regexp_replace(public.unaccent(name), '[^a-zA-Z0-9]+', '-', 'g'))), ''),
               'orateur-' || md5(name)
           ) AS base
    FROM names
)
INSERT INTO teaching_speakers (name, slug)
SELECT name,
       base || CASE
           WHEN row_number() OVER (PARTITION BY base ORDER BY name) = 1 THEN ''
           ELSE '-' || row_number() OVER (PARTITION BY base ORDER BY name)
       END
FROM prepared
ON CONFLICT (slug) DO NOTHING;

-- Their teachings find them back, accents and case left aside.
UPDATE teachings t
SET speaker_id = s.id
FROM teaching_speakers s
WHERE t.speaker_id IS NULL
  AND btrim(t.speaker) <> ''
  AND lower(public.unaccent(btrim(t.speaker))) = lower(public.unaccent(btrim(s.name)));

-- A teaching saved with a speaker id takes the speaker's name in its text
-- column, and its searchable text with it.
CREATE OR REPLACE FUNCTION teachings_before_save()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    -- the old text columns stay in step for the apps that still read them
    IF NEW.category_id IS NOT NULL THEN
        SELECT name INTO NEW.category FROM teaching_categories WHERE id = NEW.category_id;
    END IF;
    IF NEW.speaker_id IS NOT NULL THEN
        SELECT name INTO NEW.speaker FROM teaching_speakers WHERE id = NEW.speaker_id;
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

-- A speaker renamed: their teachings take the new name, and the search finds
-- it at once.
CREATE OR REPLACE FUNCTION refresh_teachings_for_speaker()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE teachings
    SET speaker = NEW.name,
        search_text = teaching_search_text(teachings)
    WHERE speaker_id = NEW.id;
    RETURN NEW;
END;
$$;

CREATE TRIGGER teaching_speakers_refresh
AFTER UPDATE OF name ON teaching_speakers
FOR EACH ROW EXECUTE FUNCTION refresh_teachings_for_speaker();

-- The list of teachings with what the API shows: their sound, the category,
-- the speaker's slug and the edition. The view gains columns, so it is
-- rebuilt (nothing else depends on it than the API's queries).
DROP VIEW teaching_details;

CREATE VIEW teaching_details AS
SELECT t.*,
       m.key AS audio_key,
       m.content_type AS audio_type,
       m.size_bytes AS audio_size,
       c.slug AS category_slug,
       c.name AS category_name,
       e.title AS edition_title,
       e.theme AS edition_theme,
       s.slug AS speaker_slug
FROM teachings t
JOIN media m ON m.id = t.audio_id
LEFT JOIN teaching_categories c ON c.id = t.category_id
LEFT JOIN teaching_editions e ON e.id = t.edition_id
LEFT JOIN teaching_speakers s ON s.id = t.speaker_id;
