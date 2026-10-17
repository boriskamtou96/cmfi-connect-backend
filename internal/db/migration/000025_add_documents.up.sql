-- Nos documents: the library of the community (teachings, rules, forms…),
-- a PDF or a Word file each, put in a category by the admins. The published
-- ones are seen by every member, the newest first.
CREATE TABLE documents (
    id BIGSERIAL PRIMARY KEY,
    title TEXT NOT NULL CHECK (btrim(title) <> ''),
    description TEXT NOT NULL DEFAULT '',
    -- "Enseignements", "Formulaires"…; '' when none
    category TEXT NOT NULL DEFAULT '',
    -- the file itself (media: a PDF, a .docx or a .doc), kept while listed
    file_id BIGINT NOT NULL REFERENCES media(id) ON DELETE RESTRICT,
    -- the name it had when sent ("Règlement intérieur.pdf"), for downloads
    file_name TEXT NOT NULL DEFAULT '',
    published BOOLEAN NOT NULL DEFAULT FALSE,
    -- set the first time it is published: the order of the list
    published_at TIMESTAMPTZ,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX documents_published_idx ON documents (published, published_at DESC);
