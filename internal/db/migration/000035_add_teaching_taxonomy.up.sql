-- Enseignements: des catégories stables (UMPJ, Camps des agneaux, Camp des
-- jeunes, Camp des leaders, Retraite des dirigeants…), une édition par année
-- et un numéro de jour pour les rencontres qui durent plusieurs jours.
-- Plusieurs enseignements peuvent avoir lieu le même jour: rien ne l'interdit,
-- [day_number] et [position] les rangent seulement.

CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE teaching_categories (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL CHECK (btrim(name) <> ''),
    -- "camp-des-jeunes": the stable name in the addresses and the API
    slug        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    -- a color ("#1F3A5F") and a short icon name, for the apps
    color       TEXT NOT NULL DEFAULT '',
    icon        TEXT NOT NULL DEFAULT '',
    -- "Camps" › "Camp des jeunes"; null for a top-level category
    parent_id   BIGINT REFERENCES teaching_categories(id) ON DELETE SET NULL,
    -- held every year (camps, retreats, crusades): the form asks for the year
    annual      BOOLEAN NOT NULL DEFAULT FALSE,
    position    INT NOT NULL DEFAULT 0,
    -- an archived category keeps its teachings and disappears from the forms
    archived_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX teaching_categories_name_active
    ON teaching_categories (lower(btrim(name)))
    WHERE archived_at IS NULL;

CREATE INDEX teaching_categories_parent_idx ON teaching_categories (parent_id);

-- One edition per year of an annual category: "Camp des jeunes 2026".
CREATE TABLE teaching_editions (
    id          BIGSERIAL PRIMARY KEY,
    category_id BIGINT NOT NULL REFERENCES teaching_categories(id) ON DELETE RESTRICT,
    year        SMALLINT NOT NULL CHECK (year BETWEEN 1900 AND 2200),
    title       TEXT NOT NULL DEFAULT '',
    theme       TEXT NOT NULL DEFAULT '',
    starts_on   DATE,
    ends_on     DATE,
    venue       TEXT NOT NULL DEFAULT '',
    cover_id    BIGINT REFERENCES media(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (category_id, year),
    CHECK (ends_on IS NULL OR starts_on IS NULL OR ends_on >= starts_on)
);

CREATE INDEX teaching_editions_category_idx ON teaching_editions (category_id, year DESC);

ALTER TABLE teachings
    ADD COLUMN category_id BIGINT REFERENCES teaching_categories(id) ON DELETE SET NULL,
    ADD COLUMN edition_id BIGINT REFERENCES teaching_editions(id) ON DELETE SET NULL,
    -- the year of the message; taken from the edition or from recorded_on
    ADD COLUMN year SMALLINT CHECK (year BETWEEN 1900 AND 2200),
    -- day 1, 2… of a camp or a crusade; several teachings may share it
    ADD COLUMN day_number SMALLINT CHECK (day_number IS NULL OR day_number > 0),
    -- their order inside the day
    ADD COLUMN position INT NOT NULL DEFAULT 0,
    -- title, speaker, description, category, edition and day, without accents
    ADD COLUMN search_text TEXT NOT NULL DEFAULT '';

CREATE INDEX teachings_category_idx ON teachings (category_id) WHERE category_id IS NOT NULL;
CREATE INDEX teachings_edition_idx ON teachings (edition_id, day_number, position) WHERE edition_id IS NOT NULL;
CREATE INDEX teachings_year_idx ON teachings (year) WHERE year IS NOT NULL;
CREATE INDEX teachings_search_trgm ON teachings USING gin (search_text gin_trgm_ops);

-- The searchable text of a teaching. Kept by a trigger so that a renamed
-- category or edition is found at once.
CREATE OR REPLACE FUNCTION teaching_search_text(t teachings)
RETURNS text
LANGUAGE sql
STABLE
AS $$
    SELECT lower(public.unaccent(concat_ws(' ',
        t.title,
        t.speaker,
        t.description,
        t.category,
        (SELECT c.name FROM teaching_categories c WHERE c.id = t.category_id),
        (SELECT e.title FROM teaching_editions e WHERE e.id = t.edition_id),
        (SELECT e.theme FROM teaching_editions e WHERE e.id = t.edition_id),
        (SELECT e.year::text FROM teaching_editions e WHERE e.id = t.edition_id),
        t.year::text,
        CASE WHEN t.day_number IS NOT NULL THEN 'jour ' || t.day_number ELSE '' END
    )));
$$;

CREATE OR REPLACE FUNCTION teachings_before_save()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    -- the old text column stays in step for the apps that still read it
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

CREATE TRIGGER teachings_before_save
BEFORE INSERT OR UPDATE ON teachings
FOR EACH ROW EXECUTE FUNCTION teachings_before_save();

-- A category renamed: its teachings keep the old text otherwise.
CREATE OR REPLACE FUNCTION refresh_teachings_for_category()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE teachings
    SET category = NEW.name,
        search_text = teaching_search_text(teachings)
    WHERE category_id = NEW.id;
    RETURN NEW;
END;
$$;

CREATE TRIGGER teaching_categories_refresh
AFTER UPDATE OF name ON teaching_categories
FOR EACH ROW EXECUTE FUNCTION refresh_teachings_for_category();

-- An edition renamed or moved: same thing for its teachings.
CREATE OR REPLACE FUNCTION refresh_teachings_for_edition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE teachings
    SET category_id = NEW.category_id,
        year = NEW.year,
        search_text = teaching_search_text(teachings)
    WHERE edition_id = NEW.id;
    RETURN NEW;
END;
$$;

CREATE TRIGGER teaching_editions_refresh
AFTER UPDATE OF category_id, year, title, theme ON teaching_editions
FOR EACH ROW EXECUTE FUNCTION refresh_teachings_for_edition();

-- The list of teachings with what the API shows: their sound, the category
-- slug and the edition. One row type for the list and for one teaching.
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

-- The categories the community already knows.
INSERT INTO teaching_categories (name, slug, annual, position) VALUES
    ('UMPJ', 'umpj', TRUE, 10),
    ('Camps des agneaux', 'camps-des-agneaux', TRUE, 20),
    ('Camp des jeunes', 'camp-des-jeunes', TRUE, 30),
    ('Camp des leaders', 'camp-des-leaders', TRUE, 40),
    ('Retraite des dirigeants du Cameroun', 'retraite-des-dirigeants-du-cameroun', TRUE, 50),
    ('Croisades de prière et de jeûne', 'croisades-de-priere-et-de-jeune', TRUE, 60),
    ('Prédications', 'predications', FALSE, 70),
    ('Autres', 'autres', FALSE, 100)
ON CONFLICT (slug) DO NOTHING;

-- The free-text categories already typed become real categories, once each.
WITH names AS (
    SELECT DISTINCT btrim(category) AS name
    FROM teachings
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
INSERT INTO teaching_categories (name, slug, annual, position)
SELECT name,
       base || CASE
           WHEN row_number() OVER (PARTITION BY base ORDER BY name) = 1 THEN ''
           ELSE '-' || row_number() OVER (PARTITION BY base ORDER BY name)
       END,
       FALSE,
       100
FROM prepared
ON CONFLICT (slug) DO NOTHING;

UPDATE teachings t
SET category_id = c.id
FROM teaching_categories c
WHERE t.category_id IS NULL
  AND lower(btrim(t.category)) = lower(btrim(c.name));

UPDATE teachings
SET year = EXTRACT(YEAR FROM recorded_on)::smallint
WHERE year IS NULL AND recorded_on IS NOT NULL;

UPDATE teachings SET search_text = teaching_search_text(teachings);
