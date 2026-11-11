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

-- name: ListActiveMembersPerWeek :many
-- Per week (starting on Monday): the members who filled a report or made a
-- retreat, and the reports filled.
SELECT date_trunc('week', a.day)::date AS week,
       COUNT(DISTINCT a.user_id)::bigint AS members,
       COUNT(*) FILTER (WHERE a.is_report)::bigint AS reports
FROM (
    SELECT user_id, report_date AS day, true AS is_report
    FROM daily_reports WHERE report_date >= sqlc.arg(since)::date
    UNION ALL
    SELECT user_id, day, false AS is_report
    FROM prayer_retreats WHERE day >= sqlc.arg(since)::date
) AS a
GROUP BY 1
ORDER BY 1;

-- name: ListNewMembersPerWeek :many
SELECT date_trunc('week', created_at AT TIME ZONE 'UTC')::date AS week, COUNT(*)::bigint AS members
FROM users
WHERE created_at >= sqlc.arg(since)
GROUP BY 1
ORDER BY 1;

-- name: ListFeatureUsage :many
-- How many members used each feature since [since]: counts only, never who.
SELECT f.feature::text AS feature, COUNT(DISTINCT f.user_id)::bigint AS members
FROM (
    SELECT 'reports' AS feature, user_id FROM daily_reports WHERE report_date >= sqlc.arg(since)::date
    UNION ALL
    SELECT 'retreats', user_id FROM prayer_retreats WHERE day >= sqlc.arg(since)::date
    UNION ALL
    SELECT 'reading', user_id FROM reading_books WHERE updated_at >= sqlc.arg(since)::date
    UNION ALL
    SELECT 'souls', user_id FROM souls WHERE created_at >= sqlc.arg(since)::date
    UNION ALL
    SELECT 'prayer_lists', user_id FROM prayer_people WHERE created_at >= sqlc.arg(since)::date
       OR last_prayed_on >= sqlc.arg(since)::date
    UNION ALL
    SELECT 'importunities', user_id FROM importunity_members
    WHERE status = 'joined' AND COALESCE(joined_at, created_at) >= sqlc.arg(since)::date
    UNION ALL
    SELECT 'events', user_id FROM event_registrations WHERE created_at >= sqlc.arg(since)::date
    UNION ALL
    SELECT 'authorities', user_id FROM authorities WHERE created_at >= sqlc.arg(since)::date
    UNION ALL
    SELECT 'report_sendings', disciple_id FROM report_sendings WHERE sent_at >= sqlc.arg(since)::date
) AS f
GROUP BY f.feature
ORDER BY members DESC, feature;

-- name: CountInstalls :one
-- The phones that can be reached by a push: the installs that signed in.
SELECT COUNT(*)::bigint FROM devices;
