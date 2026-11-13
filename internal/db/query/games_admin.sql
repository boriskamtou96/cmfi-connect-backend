-- ----- the games in the back-office: drafts included -----

-- name: ListAdminGameCourses :many
SELECT c.id, c.code, c.position, c.title, c.published_at,
       (SELECT COUNT(*) FROM game_levels lv WHERE lv.course_id = c.id)::int AS levels,
       (SELECT COUNT(*) FROM game_lessons ls
        JOIN game_levels lv ON lv.id = ls.level_id
        WHERE lv.course_id = c.id)::int AS lessons,
       (SELECT COUNT(*) FROM game_questions q
        JOIN game_lessons ls ON ls.id = q.lesson_id
        JOIN game_levels lv ON lv.id = ls.level_id
        WHERE lv.course_id = c.id AND q.is_active)::int AS questions
FROM game_courses c
ORDER BY c.position, c.id;

-- name: CreateGameCourse :exec
-- A new course is a draft, after the others.
INSERT INTO game_courses (code, title, position)
SELECT @code::varchar, @title::jsonb, COALESCE(MAX(position), 0) + 1
FROM game_courses;

-- name: UpdateGameCourse :exec
UPDATE game_courses
SET title = @title,
    published_at = CASE WHEN @published::bool THEN COALESCE(published_at, @now::timestamptz) END
WHERE id = @id;

-- name: ListAdminGameLevels :many
SELECT id, position, title, description, color, published_at
FROM game_levels
WHERE course_id = @course_id
ORDER BY position, id;

-- name: ListAdminGameLessons :many
SELECT ls.id, ls.level_id, ls.position, ls.title, ls.published_at,
       (SELECT COUNT(*) FROM game_questions q WHERE q.lesson_id = ls.id AND q.is_active)::int AS questions,
       (SELECT COUNT(*) FROM game_lesson_results r WHERE r.lesson_id = ls.id)::int AS players
FROM game_lessons ls
JOIN game_levels lv ON lv.id = ls.level_id
WHERE lv.course_id = @course_id
ORDER BY lv.position, lv.id, ls.position, ls.id;

-- name: GetAdminGameLevel :one
SELECT lv.id, lv.course_id, c.code AS course_code
FROM game_levels lv
JOIN game_courses c ON c.id = lv.course_id
WHERE lv.id = @id;

-- name: AppendGameLevel :exec
-- A new level is a draft, after the others of its course.
INSERT INTO game_levels (course_id, position, title, description, color)
SELECT @course_id::bigint, COALESCE(MAX(position), 0) + 1, @title::jsonb, @description::jsonb, @color::varchar
FROM game_levels
WHERE course_id = @course_id::bigint;

-- name: UpdateGameLevel :exec
UPDATE game_levels
SET title = @title, description = @description, color = @color,
    published_at = CASE WHEN @published::bool THEN COALESCE(published_at, @now::timestamptz) END
WHERE id = @id;

-- name: CountPublishedGameLevels :one
SELECT COUNT(*) FROM game_levels WHERE course_id = @course_id AND published_at IS NOT NULL;

-- name: CountPublishedGameLessons :one
SELECT COUNT(*) FROM game_lessons WHERE level_id = @level_id AND published_at IS NOT NULL;

-- name: CountGameResultsOfLevel :one
SELECT COUNT(*)
FROM game_lesson_results r
JOIN game_lessons ls ON ls.id = r.lesson_id
WHERE ls.level_id = @level_id;

-- name: CountGameResultsOfLesson :one
SELECT COUNT(*) FROM game_lesson_results WHERE lesson_id = @lesson_id;

-- name: DeleteGameLevel :exec
DELETE FROM game_levels WHERE id = @id;

-- name: GetAdminGameLesson :one
SELECT ls.id, ls.level_id, ls.position, ls.title, ls.published_at,
       lv.title AS level_title, c.code AS course_code,
       (SELECT COUNT(*) FROM game_lesson_results r WHERE r.lesson_id = ls.id)::int AS players
FROM game_lessons ls
JOIN game_levels lv ON lv.id = ls.level_id
JOIN game_courses c ON c.id = lv.course_id
WHERE ls.id = @id;

-- name: AppendGameLesson :exec
INSERT INTO game_lessons (level_id, position, title)
SELECT @level_id::bigint, COALESCE(MAX(position), 0) + 1, @title::jsonb
FROM game_lessons
WHERE level_id = @level_id::bigint;

-- name: UpdateGameLesson :exec
UPDATE game_lessons
SET title = @title,
    published_at = CASE WHEN @published::bool THEN COALESCE(published_at, @now::timestamptz) END
WHERE id = @id;

-- name: DeleteGameLesson :exec
DELETE FROM game_lessons WHERE id = @id;

-- name: CountActiveGameQuestions :one
SELECT COUNT(*) FROM game_questions WHERE lesson_id = @lesson_id AND is_active;

-- name: ListAdminGameQuestions :many
-- The questions of a lesson with what the members missed of each.
SELECT q.id, q.position, q.kind, q.prompt, q.choices, q.answer, q.explanation, q.source,
       COALESCE(st.asked, 0)::int AS asked, COALESCE(st.missed, 0)::int AS missed
FROM game_questions q
LEFT JOIN game_question_stats st ON st.question_id = q.id
WHERE q.lesson_id = @lesson_id AND q.is_active
ORDER BY q.position, q.id;

-- name: GetAdminGameQuestion :one
SELECT q.id, q.lesson_id, (ls.published_at IS NOT NULL)::bool AS lesson_published
FROM game_questions q
JOIN game_lessons ls ON ls.id = q.lesson_id
WHERE q.id = @id AND q.is_active;

-- name: AppendGameQuestion :exec
INSERT INTO game_questions (lesson_id, position, kind, prompt, choices, answer, explanation, source)
SELECT @lesson_id::bigint, COALESCE(MAX(position), 0) + 1, @kind::varchar, @prompt::jsonb, @choices::jsonb,
       @answer::varchar, @explanation::jsonb, @source::varchar
FROM game_questions
WHERE lesson_id = @lesson_id::bigint;

-- name: UpdateGameQuestion :exec
UPDATE game_questions
SET kind = @kind, prompt = @prompt, choices = @choices, answer = @answer,
    explanation = @explanation, source = @source
WHERE id = @id;

-- name: DeleteGameQuestion :exec
DELETE FROM game_questions WHERE id = @id;

-- ----- the order of a list: every position is written again -----

-- name: ListGameLevelIDs :many
SELECT id FROM game_levels WHERE course_id = @course_id ORDER BY position, id;

-- name: ListGameLessonIDs :many
SELECT id FROM game_lessons WHERE level_id = @level_id ORDER BY position, id;

-- name: ListGameQuestionIDs :many
SELECT id FROM game_questions WHERE lesson_id = @lesson_id AND is_active ORDER BY position, id;

-- name: SetAsideGameLevelPositions :exec
-- (course, position) is unique: the positions leave the way first.
UPDATE game_levels SET position = -position WHERE course_id = @course_id;

-- name: SetAsideGameLessonPositions :exec
UPDATE game_lessons SET position = -position WHERE level_id = @level_id;

-- name: SetGameLevelPosition :exec
UPDATE game_levels SET position = @position WHERE id = @id;

-- name: SetGameLessonPosition :exec
UPDATE game_lessons SET position = @position WHERE id = @id;

-- name: SetGameQuestionPosition :exec
UPDATE game_questions SET position = @position WHERE id = @id;

-- ----- the numbers -----

-- name: BumpGameQuestionStats :exec
-- The first answers of a first play: one more asked per question, one more
-- missed for those of [missed_ids].
INSERT INTO game_question_stats (question_id, asked, missed)
SELECT q.id, 1, (q.id = ANY(@missed_ids::bigint[]))::int
FROM game_questions q
WHERE q.id = ANY(@question_ids::bigint[])
ORDER BY q.id
ON CONFLICT (question_id) DO UPDATE
SET asked = game_question_stats.asked + 1,
    missed = game_question_stats.missed + EXCLUDED.missed;

-- name: GetGameStats :one
SELECT
    (SELECT COUNT(*) FROM game_players WHERE total_points > 0)::int AS players,
    (SELECT COUNT(*) FROM game_players WHERE in_leaderboard)::int AS in_leaderboard,
    (SELECT COUNT(DISTINCT user_id) FROM game_plays WHERE played_at >= @since::timestamptz)::int AS players_week,
    (SELECT COUNT(*) FROM game_plays WHERE played_at >= @since::timestamptz)::int AS plays_week,
    (SELECT COUNT(*) FROM game_lesson_results)::int AS lessons_completed,
    (SELECT COUNT(*) FROM game_lesson_results
     WHERE first_completed_at >= @since::timestamptz)::int AS lessons_completed_week;

-- name: ListHardestGameQuestions :many
-- The questions missed the most at the first answer, among those asked
-- often enough to say something.
SELECT q.id, q.lesson_id, q.prompt, ls.title AS lesson_title, c.code AS course_code, st.asked, st.missed
FROM game_question_stats st
JOIN game_questions q ON q.id = st.question_id AND q.is_active
JOIN game_lessons ls ON ls.id = q.lesson_id
JOIN game_levels lv ON lv.id = ls.level_id
JOIN game_courses c ON c.id = lv.course_id
WHERE st.asked >= @min_asked AND st.missed > 0
ORDER BY st.missed::float8 / st.asked DESC, st.asked DESC, q.id
LIMIT sqlc.arg(max_rows);
