-- ----- prayer topics of the community -----

-- name: CreatePrayerTopic :one
INSERT INTO prayer_topics (
    title, category, verse, starts_on, ends_on, event_id, document_id, document_name, status, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpdatePrayerTopic :one
UPDATE prayer_topics
SET title = $2,
    category = $3,
    verse = $4,
    starts_on = $5,
    ends_on = $6,
    event_id = $7,
    document_id = $8,
    document_name = $9,
    status = $10,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetPrayerTopic :one
SELECT *
FROM prayer_topics
WHERE id = $1;

-- name: DeletePrayerTopic :execrows
DELETE FROM prayer_topics
WHERE id = $1;

-- name: ListCurrentPrayerTopics :many
-- The published topics of [today]: started and not over, the latest first.
SELECT *
FROM prayer_topics
WHERE status = 'published'
  AND starts_on <= @today::date
  AND (ends_on IS NULL OR ends_on >= @today::date)
ORDER BY starts_on DESC, id DESC;

-- name: ListPastPrayerTopics :many
SELECT *
FROM prayer_topics
WHERE status = 'published'
  AND ends_on < @today::date
ORDER BY ends_on DESC, id DESC
LIMIT @lim OFFSET @off;

-- name: ListAllPrayerTopics :many
-- For the admins: drafts included.
SELECT *
FROM prayer_topics
ORDER BY starts_on DESC, id DESC
LIMIT @lim OFFSET @off;

-- name: DeletePrayerTopicPoints :exec
DELETE FROM prayer_topic_points
WHERE topic_id = $1;

-- name: CreatePrayerTopicPoint :exec
INSERT INTO prayer_topic_points (topic_id, position, body)
VALUES ($1, $2, $3);

-- name: ListPrayerTopicPoints :many
SELECT *
FROM prayer_topic_points
WHERE topic_id = ANY(@topic_ids::bigint[])
ORDER BY topic_id, position;

-- name: CountPrayerTopicPeople :many
-- How many members prayed for each topic, any day: the apps never show who.
SELECT topic_id, count(DISTINCT user_id)::int AS people
FROM prayer_topic_prayers
WHERE topic_id = ANY(@topic_ids::bigint[])
GROUP BY topic_id;

-- name: ListMyPrayedPoints :many
SELECT topic_id, position
FROM prayer_topic_prayers
WHERE user_id = @user_id
  AND prayed_on = @day::date
  AND topic_id = ANY(@topic_ids::bigint[]);

-- name: DeleteMyPrayedPoints :exec
DELETE FROM prayer_topic_prayers
WHERE topic_id = @topic_id
  AND user_id = @user_id
  AND prayed_on = @day::date;

-- name: AddMyPrayedPoint :exec
INSERT INTO prayer_topic_prayers (topic_id, position, user_id, prayed_on)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- ----- the members' own lists -----

-- name: ListPrayerPeople :many
SELECT *
FROM prayer_people
WHERE user_id = $1
ORDER BY list, id;

-- name: CountPrayerPeople :one
SELECT count(*)
FROM prayer_people
WHERE user_id = $1;

-- name: CreatePrayerPerson :one
INSERT INTO prayer_people (user_id, list, name, note)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdatePrayerPerson :one
UPDATE prayer_people
SET list = $3,
    name = $4,
    note = $5,
    answered_on = $6
WHERE id = $1
  AND user_id = $2
RETURNING *;

-- name: DeletePrayerPerson :execrows
DELETE FROM prayer_people
WHERE id = $1
  AND user_id = $2;

-- name: MarkPrayerPeoplePrayed :execrows
-- A retreat prayed for them on [day]: the next retreats take the others first.
UPDATE prayer_people
SET last_prayed_on = @day::date
WHERE user_id = @user_id
  AND id = ANY(@ids::bigint[]);

-- ----- the 15-minute retreats -----

-- name: ListPrayerRetreats :many
SELECT *
FROM prayer_retreats
WHERE user_id = @user_id
  AND day BETWEEN @from_day::date AND @to_day::date
ORDER BY day, done_at;

-- name: UpsertPrayerRetreat :one
INSERT INTO prayer_retreats (user_id, day, moment, minutes)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, day, moment) DO UPDATE
SET minutes = EXCLUDED.minutes,
    done_at = now()
RETURNING *;

-- name: DeletePrayerRetreat :execrows
DELETE FROM prayer_retreats
WHERE user_id = $1
  AND day = $2
  AND moment = $3;

-- ----- importunity topics -----

-- name: CreateImportunity :one
INSERT INTO importunities (owner_id, title, description, goal)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetImportunity :one
SELECT *
FROM importunities
WHERE id = $1;

-- name: LockImportunity :one
SELECT *
FROM importunities
WHERE id = $1
FOR UPDATE;

-- name: UpdateImportunity :one
UPDATE importunities
SET title = $2,
    description = $3,
    goal = $4,
    status = $5,
    reached_at = $6,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: AddImportunityProclamations :one
-- The total, and the moment the goal is first reached.
UPDATE importunities
SET total = total + @amount::bigint,
    reached_at = CASE
        WHEN reached_at IS NULL AND goal IS NOT NULL AND total + @amount::bigint >= goal THEN now()
        ELSE reached_at
    END,
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: DeleteImportunity :execrows
DELETE FROM importunities
WHERE id = $1;

-- name: AddImportunityOwner :exec
INSERT INTO importunity_members (importunity_id, user_id, status, joined_at)
VALUES ($1, $2, 'joined', now());

-- name: InviteToImportunity :many
-- Already invited, joined or declined: left as they are. Gives the members
-- invited now (they get a push).
INSERT INTO importunity_members (importunity_id, user_id, invited_by)
SELECT @importunity_id::bigint, invited, @invited_by::bigint
FROM unnest(@user_ids::bigint[]) AS invited
ON CONFLICT (importunity_id, user_id) DO NOTHING
RETURNING user_id;

-- name: GetImportunityMember :one
SELECT *
FROM importunity_members
WHERE importunity_id = $1
  AND user_id = $2;

-- name: AnswerImportunityInvitation :one
UPDATE importunity_members
SET status = CASE WHEN @accept::boolean THEN 'joined' ELSE 'declined' END,
    joined_at = CASE WHEN @accept::boolean THEN now() ELSE joined_at END
WHERE importunity_id = @importunity_id
  AND user_id = @user_id
  AND status = 'invited'
RETURNING *;

-- name: AddMemberProclamations :one
UPDATE importunity_members
SET count = count + @amount::bigint
WHERE importunity_id = @importunity_id
  AND user_id = @user_id
  AND status = 'joined'
RETURNING *;

-- name: LeaveImportunity :execrows
-- Their proclamations stay in the total.
DELETE FROM importunity_members
WHERE importunity_id = $1
  AND user_id = $2;

-- name: ListMyImportunities :many
-- The topics a member prays for or is invited to: invitations first.
SELECT i.*,
       m.status AS my_status,
       m.count AS my_count,
       m.invited_by AS my_inviter
FROM importunities i
JOIN importunity_members m ON m.importunity_id = i.id
WHERE m.user_id = $1
  AND m.status <> 'declined'
ORDER BY (m.status = 'invited') DESC, i.status DESC, i.updated_at DESC;

-- name: CountImportunityMembers :many
SELECT importunity_id,
       count(*) FILTER (WHERE status = 'joined')::int AS joined,
       count(*) FILTER (WHERE status = 'invited')::int AS invited
FROM importunity_members
WHERE importunity_id = ANY(@ids::bigint[])
GROUP BY importunity_id;

-- name: ListImportunityParticipants :many
-- Who joined, in order. No counts: each member only sees their own.
SELECT user_id
FROM importunity_members
WHERE importunity_id = $1
  AND status = 'joined'
ORDER BY joined_at, user_id;

-- name: GetMyDiscipleMaker :one
-- The member who is my disciple maker (FD), once they accepted the link.
SELECT linked_user_id::bigint AS id
FROM authorities
WHERE user_id = $1
  AND is_disciple_maker
  AND status = 'accepted'
  AND linked_user_id IS NOT NULL;

-- name: ListDisciplesOf :many
-- The members whose disciple maker is [fd], accepted links only.
SELECT user_id
FROM authorities
WHERE linked_user_id = $1
  AND is_disciple_maker
  AND status = 'accepted'
ORDER BY user_id;
