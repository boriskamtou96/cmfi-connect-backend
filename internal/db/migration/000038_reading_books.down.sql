DROP INDEX IF EXISTS idx_report_entries_book;
ALTER TABLE report_entries DROP COLUMN IF EXISTS book_id;
DROP TABLE IF EXISTS reading_books;
