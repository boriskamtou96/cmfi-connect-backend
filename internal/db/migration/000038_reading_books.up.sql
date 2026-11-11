-- The books a member reads (Lecture de la littérature chrétienne): the LLC
-- item of a report says which book was read that day, and its pages add up
-- to the book's progress. One book per day: one LLC entry per report.
CREATE TABLE IF NOT EXISTS reading_books (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title VARCHAR(300) NOT NULL CHECK (btrim(title) <> ''),
    author VARCHAR(200) NOT NULL DEFAULT '',
    total_pages INT NOT NULL CHECK (total_pages BETWEEN 1 AND 20000),
    -- found by the search (Open Library), empty when typed by hand
    cover_url TEXT NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'reading'
        CHECK (status IN ('reading', 'finished', 'abandoned')),
    -- the day the member said they finished it: the books of a month
    finished_on DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_reading_books_finished CHECK ((status = 'finished') = (finished_on IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_reading_books_user ON reading_books (user_id, status);
CREATE INDEX IF NOT EXISTS idx_reading_books_finished ON reading_books (user_id, finished_on)
    WHERE finished_on IS NOT NULL;

-- the book read that day (LLC only); a deleted book leaves the pages
ALTER TABLE report_entries
    ADD COLUMN IF NOT EXISTS book_id BIGINT REFERENCES reading_books(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_report_entries_book ON report_entries (book_id) WHERE book_id IS NOT NULL;
