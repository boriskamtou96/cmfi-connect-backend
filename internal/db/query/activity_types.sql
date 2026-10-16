-- name: GetUserActivityTypes :many
-- Standard items (user_id IS NULL) + the user's own items, archived ones excluded.
SELECT * FROM activity_types
WHERE (user_id IS NULL OR user_id = sqlc.arg(user_id)::bigint)
  AND archived_at IS NULL
ORDER BY position, code;

-- name: IsStandardActivityCode :one
SELECT EXISTS (
    SELECT 1 FROM activity_types
    WHERE user_id IS NULL AND code = sqlc.arg(code)
);

-- name: CreateActivityType :one
-- Personal items are numbered after the standard ones: 101, 102...
INSERT INTO activity_types (user_id, code, label, tracks_quantity, quantity_unit, tracks_duration, position)
VALUES (
    sqlc.arg(user_id)::bigint,
    sqlc.arg(code),
    sqlc.arg(label),
    sqlc.arg(tracks_quantity),
    sqlc.narg(quantity_unit),
    sqlc.arg(tracks_duration),
    (SELECT GREATEST(COALESCE(MAX(position), 0), 100) + 1
     FROM activity_types
     WHERE user_id = sqlc.arg(user_id)::bigint)
)
RETURNING *;

-- name: UpdateActivityType :one
-- Only the user's own active item can change; standard items never match (user_id IS NULL).
-- tracks_quantity / tracks_duration stay fixed: past entries were saved with them.
UPDATE activity_types
SET code = sqlc.arg(code),
    label = sqlc.arg(label),
    quantity_unit = sqlc.narg(quantity_unit),
    position = COALESCE(sqlc.narg(position), position)
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)::bigint
  AND archived_at IS NULL
RETURNING *;

-- name: ArchiveActivityType :execrows
-- Archive instead of DELETE: report_entries keeps pointing to the item (ON DELETE RESTRICT).
UPDATE activity_types
SET archived_at = NOW()
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)::bigint
  AND archived_at IS NULL;

-- name: GetVisibleActivityType :one
-- A standard item or one of the user's own, still active.
SELECT * FROM activity_types
WHERE id = sqlc.arg(id)
  AND (user_id IS NULL OR user_id = sqlc.arg(user_id)::bigint)
  AND archived_at IS NULL;

-- name: ListActivityGoals :many
-- The user's daily goals, by item.
SELECT * FROM activity_goals
WHERE user_id = sqlc.arg(user_id);

-- name: SetActivityGoal :one
INSERT INTO activity_goals (user_id, activity_type_id, quantity, minutes)
VALUES (
    sqlc.arg(user_id),
    sqlc.arg(activity_type_id),
    sqlc.narg(quantity),
    sqlc.narg(minutes)
)
ON CONFLICT (user_id, activity_type_id) DO UPDATE
SET quantity = EXCLUDED.quantity,
    minutes = EXCLUDED.minutes,
    updated_at = NOW()
RETURNING *;

-- name: DeleteActivityGoal :exec
DELETE FROM activity_goals
WHERE user_id = sqlc.arg(user_id)
  AND activity_type_id = sqlc.arg(activity_type_id);

-- ----- the standard items, kept by the admins -----

-- name: ListStandardActivityTypes :many
-- Every standard item, the archived ones last.
SELECT * FROM activity_types
WHERE user_id IS NULL
ORDER BY archived_at IS NOT NULL, position, code;

-- name: CreateStandardActivityType :one
-- After the other standard items (positions under 100: the personal items
-- come from 101).
INSERT INTO activity_types (user_id, code, label, tracks_quantity, quantity_unit, tracks_duration, position)
VALUES (
    NULL,
    sqlc.arg(code),
    sqlc.arg(label),
    sqlc.arg(tracks_quantity),
    sqlc.narg(quantity_unit),
    sqlc.arg(tracks_duration),
    (SELECT LEAST(COALESCE(MAX(position), 0) + 1, 99)
     FROM activity_types
     WHERE user_id IS NULL AND position < 100)
)
RETURNING *;

-- name: UpdateStandardActivityType :one
-- The label, the unit and the place; the code and the measures stay (the
-- reports and the app rely on them).
UPDATE activity_types
SET label = sqlc.arg(label),
    quantity_unit = sqlc.narg(quantity_unit),
    position = COALESCE(sqlc.narg(position), position)
WHERE id = sqlc.arg(id)
  AND user_id IS NULL
RETURNING *;

-- name: SetStandardActivityTypeArchived :one
-- Archived: no longer offered in the reports; the past entries stay.
UPDATE activity_types
SET archived_at = CASE WHEN sqlc.arg(archived)::bool THEN COALESCE(archived_at, NOW()) ELSE NULL END
WHERE id = sqlc.arg(id)
  AND user_id IS NULL
RETURNING *;

-- name: CountPersonalItemsWithCode :one
-- Members whose own active item already has [code].
SELECT COUNT(*)::bigint FROM activity_types
WHERE user_id IS NOT NULL
  AND code = sqlc.arg(code)
  AND archived_at IS NULL;
