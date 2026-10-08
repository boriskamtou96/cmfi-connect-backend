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
