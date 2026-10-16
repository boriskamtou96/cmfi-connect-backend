-- The numbers of the back office's dashboard: counts only, never who.

-- name: CountMembers :one
SELECT COUNT(*)::bigint AS total,
       COUNT(*) FILTER (WHERE created_at >= sqlc.arg(week_start))::bigint AS new_this_week,
       COUNT(*) FILTER (WHERE created_at >= sqlc.arg(month_start))::bigint AS new_this_month
FROM users;

-- name: CountActiveMembers :one
-- Members who filled a report or made a retreat since [since].
SELECT COUNT(DISTINCT active.user_id)::bigint
FROM (
    SELECT user_id FROM daily_reports WHERE report_date >= sqlc.arg(since)::date
    UNION
    SELECT user_id FROM prayer_retreats WHERE day >= sqlc.arg(since)::date
) AS active;

-- name: ListReportsPerDay :many
SELECT report_date::date AS day, COUNT(DISTINCT user_id)::bigint AS members
FROM daily_reports
WHERE report_date >= sqlc.arg(since)::date
GROUP BY report_date
ORDER BY report_date;

-- name: ListRetreatsPerDay :many
SELECT day::date AS day, COUNT(*)::bigint AS retreats, COUNT(DISTINCT user_id)::bigint AS members
FROM prayer_retreats
WHERE day >= sqlc.arg(since)::date
GROUP BY day
ORDER BY day;

-- name: ListNewMembersPerDay :many
SELECT (created_at AT TIME ZONE 'UTC')::date AS day, COUNT(*)::bigint AS members
FROM users
WHERE created_at >= sqlc.arg(since)
GROUP BY 1
ORDER BY 1;

-- name: ListMembersByCountry :many
-- The countries of the profiles ('' when not given), the largest first.
SELECT COALESCE(p.country, '')::text AS country, COUNT(*)::bigint AS members
FROM users u
LEFT JOIN profiles p ON p.user_id = u.id
GROUP BY 1
ORDER BY members DESC, country
LIMIT sqlc.arg(max_rows);
