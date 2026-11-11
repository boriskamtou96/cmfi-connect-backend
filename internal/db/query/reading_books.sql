-- name: CreateReadingBook :one
INSERT INTO reading_books (user_id, title, author, total_pages, cover_url)
VALUES (sqlc.arg(user_id), sqlc.arg(title), sqlc.arg(author), sqlc.arg(total_pages), sqlc.arg(cover_url))
RETURNING id;

-- name: ListReadingBooks :many
-- The member's books with their progress: the pages of the reports that name
-- them, and the first day one did (else the day the book was added). Books
-- being read first, then the finished ones, latest first.
SELECT rb.id, rb.title, rb.author, rb.total_pages, rb.cover_url, rb.status, rb.finished_on, rb.created_at,
       COALESCE(p.pages, 0)::int AS pages_read,
       COALESCE(p.first_day, rb.created_at::date)::date AS started_on
FROM reading_books rb
LEFT JOIN (
    SELECT re.book_id, SUM(re.quantity) AS pages, MIN(dr.report_date) AS first_day
    FROM report_entries re
    JOIN daily_reports dr ON dr.id = re.report_id
    WHERE dr.user_id = sqlc.arg(user_id)::bigint AND re.book_id IS NOT NULL
    GROUP BY re.book_id
) p ON p.book_id = rb.id
WHERE rb.user_id = sqlc.arg(user_id)::bigint
ORDER BY rb.status = 'reading' DESC, rb.finished_on DESC NULLS LAST, rb.created_at DESC, rb.id DESC;

-- name: GetReadingBookOwner :one
SELECT user_id FROM reading_books WHERE id = sqlc.arg(id);

-- name: UpdateReadingBook :execrows
UPDATE reading_books
SET title = sqlc.arg(title), author = sqlc.arg(author), total_pages = sqlc.arg(total_pages),
    cover_url = sqlc.arg(cover_url), updated_at = NOW()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: SetReadingBookStatus :execrows
-- finished_on is set with 'finished' only (the table checks it).
UPDATE reading_books
SET status = sqlc.arg(status), finished_on = sqlc.narg(finished_on), updated_at = NOW()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: DeleteReadingBook :execrows
DELETE FROM reading_books WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: CountFinishedBooksByMonth :many
-- The books finished each month between two days (months without any are left out).
SELECT date_trunc('month', finished_on)::date AS month, COUNT(*)::int AS books
FROM reading_books
WHERE user_id = sqlc.arg(user_id)
  AND finished_on BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
GROUP BY 1
ORDER BY 1;

-- name: CountFinishedBooks :one
SELECT COUNT(*)::int FROM reading_books
WHERE user_id = sqlc.arg(user_id)
  AND finished_on BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date;

-- name: SumBookPages :one
-- The pages read in books between two days.
SELECT COALESCE(SUM(re.quantity), 0)::int
FROM report_entries re
JOIN daily_reports dr ON dr.id = re.report_id
WHERE dr.user_id = sqlc.arg(user_id)::bigint
  AND re.book_id IS NOT NULL
  AND dr.report_date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date;
