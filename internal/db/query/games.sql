-- ----- the content: courses, levels, lessons, questions -----

-- name: ListGameCourses :many
-- The published courses, in the order of the tab.
SELECT id, code, position, title
FROM game_courses
WHERE published_at IS NOT NULL
ORDER BY position, id;

-- name: ListGameLevelsOfCourse :many
SELECT id, position, title, description, color
FROM game_levels
WHERE course_id = @course_id AND published_at IS NOT NULL
ORDER BY position, id;

-- name: ListGameLessonsOfCourse :many
-- The lessons of a course in the order of its path, with what [user_id] did
-- of each: a lesson opens once the one before it is done.
SELECT ls.id, ls.level_id, ls.position, ls.title,
       COALESCE(r.best_stars, 0)::int AS stars,
       (r.user_id IS NOT NULL)::bool AS done
FROM game_lessons ls
JOIN game_levels lv ON lv.id = ls.level_id
LEFT JOIN game_lesson_results r ON r.lesson_id = ls.id AND r.user_id = @user_id::bigint
WHERE lv.course_id = @course_id
  AND lv.published_at IS NOT NULL
  AND ls.published_at IS NOT NULL
ORDER BY lv.position, lv.id, ls.position, ls.id;

-- name: GetGameLesson :one
-- A lesson the members can see: published, like its level and its course.
SELECT ls.id, ls.title, lv.title AS level_title, lv.course_id, c.code AS course_code
FROM game_lessons ls
JOIN game_levels lv ON lv.id = ls.level_id
JOIN game_courses c ON c.id = lv.course_id
WHERE ls.id = @id
  AND ls.published_at IS NOT NULL
  AND lv.published_at IS NOT NULL
  AND c.published_at IS NOT NULL;

-- name: ListGameQuestionsOfLesson :many
SELECT id, kind, prompt, choices, answer, explanation, source
FROM game_questions
WHERE lesson_id = @lesson_id AND is_active
ORDER BY position, id;

-- name: ListCompletedGameQuestions :many
-- The questions of the lessons [user_id] finished: the challenge of the day
-- draws from them.
SELECT q.id, q.kind, q.prompt, q.choices, q.answer, q.explanation, q.source
FROM game_questions q
JOIN game_lessons ls ON ls.id = q.lesson_id AND ls.published_at IS NOT NULL
JOIN game_lesson_results r ON r.lesson_id = q.lesson_id AND r.user_id = @user_id
WHERE q.is_active
ORDER BY q.id;

-- ----- the plays -----

-- name: GetGamePlayByClientID :one
-- A play already received (the phone sends it again on a weak network).
SELECT * FROM game_plays
WHERE user_id = @user_id AND client_id = @client_id;

-- name: CreateGamePlay :one
INSERT INTO game_plays (user_id, lesson_id, kind, client_id, first_try, total, stars, points, combo_bonus, level_up, played_at)
VALUES (@user_id, sqlc.narg(lesson_id), @kind, @client_id, @first_try, @total, @stars, @points, @combo_bonus, @level_up, @played_at)
RETURNING *;

-- name: HasGameLessonResult :one
SELECT EXISTS (
    SELECT 1 FROM game_lesson_results WHERE user_id = @user_id AND lesson_id = @lesson_id
);

-- name: GetGameLessonBestStars :one
SELECT best_stars FROM game_lesson_results
WHERE user_id = @user_id AND lesson_id = @lesson_id;

-- name: UpsertGameLessonResult :one
-- Keeps the best play of the lesson.
INSERT INTO game_lesson_results (user_id, lesson_id, best_stars, best_first_try, plays, first_completed_at, last_played_at)
VALUES (@user_id, @lesson_id, @stars, @first_try, 1, @played_at, @played_at)
ON CONFLICT (user_id, lesson_id) DO UPDATE
SET best_stars = GREATEST(game_lesson_results.best_stars, EXCLUDED.best_stars),
    best_first_try = GREATEST(game_lesson_results.best_first_try, EXCLUDED.best_first_try),
    plays = game_lesson_results.plays + 1,
    last_played_at = EXCLUDED.last_played_at
RETURNING best_stars;

-- name: SumGamePointsSince :one
-- The points of [user_id] since [since]: the goal of the day.
SELECT COALESCE(SUM(points), 0)::int AS points
FROM game_plays
WHERE user_id = @user_id AND played_at >= @since;

-- name: SumGameReplayPointsSince :one
-- The points of the lessons played again since [since]: capped by day.
SELECT COALESCE(SUM(points), 0)::int AS points
FROM game_plays
WHERE user_id = @user_id AND kind = 'replay' AND played_at >= @since;

-- ----- the players -----

-- name: GetGamePlayer :one
SELECT * FROM game_players WHERE user_id = @user_id;

-- name: EnsureGamePlayer :exec
INSERT INTO game_players (user_id) VALUES (@user_id)
ON CONFLICT (user_id) DO NOTHING;

-- name: LockGamePlayer :one
-- Inside the transaction of a play: two plays of one member wait in line.
SELECT * FROM game_players WHERE user_id = @user_id FOR UPDATE;

-- name: SaveGamePlayerProgress :exec
UPDATE game_players
SET total_points = @total_points,
    streak_days = @streak_days,
    last_played_on = sqlc.narg(last_played_on),
    grace_used_on = sqlc.narg(grace_used_on),
    daily_done_on = sqlc.narg(daily_done_on)
WHERE user_id = @user_id;

-- name: SaveGamePlayerSettings :exec
UPDATE game_players
SET in_leaderboard = sqlc.narg(in_leaderboard), daily_goal = @daily_goal
WHERE user_id = @user_id;

-- ----- the leaderboard -----

-- name: ListGameLeaderboard :many
-- The 50 first of those who chose to appear, over a period ([since] NULL:
-- since ever) and a scope ([ids] empty: every member). On a tie, whoever got
-- there first. [viewer_id] sees neither those they blocked nor those who
-- blocked them.
SELECT u.id, u.username, s.points::int AS points, s.reached_at::timestamptz AS reached_at
FROM (
    SELECT gp.user_id, SUM(gp.points) AS points, MAX(gp.played_at) FILTER (WHERE gp.points > 0) AS reached_at
    FROM game_plays gp
    WHERE (sqlc.narg(since)::timestamptz IS NULL OR gp.played_at >= sqlc.narg(since)::timestamptz)
    GROUP BY gp.user_id
) s
JOIN game_players p ON p.user_id = s.user_id AND p.in_leaderboard
JOIN users u ON u.id = s.user_id
WHERE s.points > 0
  AND (COALESCE(cardinality(@ids::bigint[]), 0) = 0 OR s.user_id = ANY(@ids::bigint[]))
  AND NOT EXISTS (
      SELECT 1 FROM member_blocks b
      WHERE (b.blocker_id = @viewer_id::bigint AND b.blocked_id = s.user_id)
         OR (b.blocker_id = s.user_id AND b.blocked_id = @viewer_id::bigint)
  )
ORDER BY s.points DESC, s.reached_at, s.user_id
LIMIT 50;

-- name: GetGameStanding :one
-- The points of [user_id] over the period, and when they got there.
SELECT COALESCE(SUM(points), 0)::int AS points,
       COALESCE(MAX(played_at) FILTER (WHERE points > 0), 'epoch'::timestamptz)::timestamptz AS reached_at
FROM game_plays
WHERE user_id = @user_id
  AND (sqlc.narg(since)::timestamptz IS NULL OR played_at >= sqlc.narg(since)::timestamptz);

-- name: CountGameLeadersAhead :one
-- How many of the leaderboard stand before [points] reached at [reached_at]:
-- the rank is one more.
SELECT COUNT(*)
FROM (
    SELECT gp.user_id, SUM(gp.points) AS points, MAX(gp.played_at) FILTER (WHERE gp.points > 0) AS reached_at
    FROM game_plays gp
    WHERE (sqlc.narg(since)::timestamptz IS NULL OR gp.played_at >= sqlc.narg(since)::timestamptz)
    GROUP BY gp.user_id
) s
JOIN game_players p ON p.user_id = s.user_id AND p.in_leaderboard
WHERE s.points > 0
  AND s.user_id <> @viewer_id::bigint
  AND (COALESCE(cardinality(@ids::bigint[]), 0) = 0 OR s.user_id = ANY(@ids::bigint[]))
  AND NOT EXISTS (
      SELECT 1 FROM member_blocks b
      WHERE (b.blocker_id = @viewer_id::bigint AND b.blocked_id = s.user_id)
         OR (b.blocker_id = s.user_id AND b.blocked_id = @viewer_id::bigint)
  )
  AND (s.points > @points::int
       OR (s.points = @points::int AND s.reached_at < @reached_at::timestamptz)
       OR (s.points = @points::int AND s.reached_at = @reached_at::timestamptz AND s.user_id < @viewer_id::bigint));

-- name: ListGameCircleIDs :many
-- The circle of [user_id]: their authorities on the app and their disciples.
SELECT linked_user_id::bigint AS member_id
FROM authorities
WHERE user_id = @user_id::bigint AND status = 'accepted' AND linked_user_id IS NOT NULL
UNION
SELECT user_id AS member_id
FROM authorities
WHERE linked_user_id = @user_id::bigint AND status = 'accepted';

-- ----- the import of a course (admins) -----

-- name: UpsertGameCourse :one
INSERT INTO game_courses (code, position, title, published_at)
VALUES (@code, @position, @title, sqlc.narg(published_at))
ON CONFLICT (code) DO UPDATE
SET position = EXCLUDED.position, title = EXCLUDED.title, published_at = EXCLUDED.published_at
RETURNING id;

-- name: CountGameResultsOfCourse :one
-- The lessons of the course already played by someone: importing it again
-- would erase their progress.
SELECT COUNT(*)
FROM game_lesson_results r
JOIN game_lessons ls ON ls.id = r.lesson_id
JOIN game_levels lv ON lv.id = ls.level_id
JOIN game_courses c ON c.id = lv.course_id
WHERE c.code = @code;

-- name: DeleteGameLevelsOfCourse :exec
DELETE FROM game_levels WHERE course_id = @course_id;

-- name: CreateGameLevel :one
INSERT INTO game_levels (course_id, position, title, description, color, published_at)
VALUES (@course_id, @position, @title, @description, @color, sqlc.narg(published_at))
RETURNING id;

-- name: CreateGameLesson :one
INSERT INTO game_lessons (level_id, position, title, published_at)
VALUES (@level_id, @position, @title, sqlc.narg(published_at))
RETURNING id;

-- name: CreateGameQuestion :exec
INSERT INTO game_questions (lesson_id, position, kind, prompt, choices, answer, explanation, source)
VALUES (@lesson_id, @position, @kind, @prompt, @choices, @answer, @explanation, @source);

-- name: DeleteGameCourse :exec
-- The course with its levels, lessons, questions and what was played of it.
DELETE FROM game_courses WHERE code = @code;

-- name: GetGameCourseByCode :one
SELECT id, code, position, title, published_at
FROM game_courses
WHERE code = @code;
