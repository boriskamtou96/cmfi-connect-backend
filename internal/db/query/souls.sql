-- name: ListSouls :many
SELECT *
FROM souls
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: UpsertSoul :one
INSERT INTO souls (user_id, id, name, phone, place, met_on, stage, next_contact, notes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (user_id, id) DO UPDATE
SET name = EXCLUDED.name,
    phone = EXCLUDED.phone,
    place = EXCLUDED.place,
    met_on = EXCLUDED.met_on,
    stage = EXCLUDED.stage,
    next_contact = EXCLUDED.next_contact,
    notes = EXCLUDED.notes,
    updated_at = now()
RETURNING *;

-- name: DeleteSoul :exec
DELETE FROM souls
WHERE user_id = $1 AND id = $2;
