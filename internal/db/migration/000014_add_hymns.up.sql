-- Hymn books: Rendons ministère au Seigneur, Christ est vainqueur, Christ is victory...
-- updated_at changes each time an import changes the book or one of its hymns:
-- the app uses it as the version of its offline copy.
CREATE TABLE IF NOT EXISTS hymn_books (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(50) NOT NULL UNIQUE,
    title VARCHAR(200) NOT NULL,
    language VARCHAR(10) NOT NULL DEFAULT 'fr',
    position INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- A hymn of a book. parts holds the verses and choruses in book order:
-- [{"kind": "verse", "label": "1", "lines": ["...", "..."]}, {"kind": "chorus", "lines": [...]}]
CREATE TABLE IF NOT EXISTS hymns (
    id BIGSERIAL PRIMARY KEY,
    book_id BIGINT NOT NULL REFERENCES hymn_books(id) ON DELETE CASCADE,
    number INT NOT NULL CHECK (number > 0),
    title VARCHAR(300) NOT NULL,
    -- words and music credits as printed under the hymn: "P. FOKA (P&M) 1985."
    author TEXT NOT NULL DEFAULT '',
    parts JSONB NOT NULL CHECK (jsonb_typeof(parts) = 'array'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (book_id, number)
);
