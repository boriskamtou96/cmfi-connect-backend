-- name: GetDailyReport :one
SELECT * FROM daily_reports
WHERE user_id = sqlc.arg(user_id) AND report_date = sqlc.arg(report_date);

-- name: GetDailyReportForm :many
-- Every item the user can fill in, with the values entered that day (NULL when empty).
SELECT at.id AS activity_type_id, at.code, at.label, at.tracks_quantity, at.quantity_unit, at.tracks_duration,
       re.quantity, re.duration_minutes
FROM activity_types at
LEFT JOIN daily_reports dr ON dr.user_id = sqlc.arg(user_id)::bigint AND dr.report_date = sqlc.arg(report_date)::date
LEFT JOIN report_entries re ON re.report_id = dr.id AND re.activity_type_id = at.id
WHERE (at.user_id IS NULL OR at.user_id = sqlc.arg(user_id)::bigint)
  AND at.archived_at IS NULL
ORDER BY at.position, at.code;

-- name: UpsertDailyReport :one
-- Creates the day, or updates its note when it already exists.
INSERT INTO daily_reports (user_id, report_date, note)
VALUES (sqlc.arg(user_id), sqlc.arg(report_date), sqlc.narg(note))
ON CONFLICT (user_id, report_date)
DO UPDATE SET note = EXCLUDED.note, updated_at = NOW()
RETURNING *;

-- name: DeleteReportEntries :exec
DELETE FROM report_entries
WHERE report_id = sqlc.arg(report_id);

-- name: CreateReportEntry :exec
INSERT INTO report_entries (report_id, activity_type_id, quantity, duration_minutes)
VALUES (sqlc.arg(report_id), sqlc.arg(activity_type_id), sqlc.narg(quantity), sqlc.narg(duration_minutes));

-- name: ListDailyReportEntries :many
-- History: one row per (day, entry). A day with only a note gives one row with NULL entry columns.
-- Archived items still show here: past values are never lost.
SELECT dr.report_date, dr.note,
       re.activity_type_id, at.code, at.label, at.quantity_unit, re.quantity, re.duration_minutes
FROM daily_reports dr
LEFT JOIN report_entries re ON re.report_id = dr.id
LEFT JOIN activity_types at ON at.id = re.activity_type_id
WHERE dr.user_id = sqlc.arg(user_id)::bigint
  AND dr.report_date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
ORDER BY dr.report_date DESC, at.position, at.code;

-- name: GetReportSummary :many
-- Totals per available item. The CTE keeps only this user's entries before the LEFT JOIN:
-- standard items are shared, joining report_entries directly would mix users.
WITH entries AS (
    SELECT re.activity_type_id, re.quantity, re.duration_minutes
    FROM report_entries re
    JOIN daily_reports dr ON dr.id = re.report_id
    WHERE dr.user_id = sqlc.arg(user_id)::bigint
      AND dr.report_date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
)
SELECT at.id AS activity_type_id, at.code, at.label, at.quantity_unit, at.tracks_quantity, at.tracks_duration,
       COUNT(e.activity_type_id) AS days_count,
       COALESCE(SUM(e.quantity), 0)::bigint AS total_quantity,
       COALESCE(SUM(e.duration_minutes), 0)::bigint AS total_minutes
FROM activity_types at
LEFT JOIN entries e ON e.activity_type_id = at.id
WHERE (at.user_id IS NULL OR at.user_id = sqlc.arg(user_id)::bigint)
  AND at.archived_at IS NULL
GROUP BY at.id
ORDER BY at.position, at.code;

-- name: CountReportedDays :one
SELECT COUNT(*) FROM daily_reports
WHERE user_id = sqlc.arg(user_id)::bigint
  AND report_date BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date;
