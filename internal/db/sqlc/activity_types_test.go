package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/go-faker/faker/v4"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func randomActivityCode() string {
	return fmt.Sprintf("T%08d", rand.Intn(100_000_000))
}

func createRandomActivityType(t *testing.T, userID int64) ActivityType {
	params := CreateActivityTypeParams{
		UserID:         userID,
		Code:           randomActivityCode(),
		Label:          faker.Word(),
		TracksQuantity: true,
		QuantityUnit: sql.NullString{
			String: "fois",
			Valid:  true,
		},
		TracksDuration: true,
	}

	activityType, err := testQueries.CreateActivityType(context.Background(), params)
	require.NoError(t, err)
	require.NotEmpty(t, activityType)

	require.True(t, activityType.UserID.Valid)
	require.Equal(t, userID, activityType.UserID.Int64)
	require.Equal(t, params.Code, activityType.Code)
	require.Equal(t, params.Label, activityType.Label)
	require.Equal(t, params.TracksQuantity, activityType.TracksQuantity)
	require.Equal(t, params.QuantityUnit, activityType.QuantityUnit)
	require.Equal(t, params.TracksDuration, activityType.TracksDuration)
	require.False(t, activityType.ArchivedAt.Valid)
	require.NotZero(t, activityType.CreatedAt)

	return activityType
}

// withRollback runs fn in a transaction that is always rolled back. Used for standard
// items: they are visible to every user, so a test must not leave one behind.
func withRollback(t *testing.T, fn func(tx *sql.Tx, q *Queries)) {
	conn, ok := testQueries.db.(*sql.DB)
	require.True(t, ok)

	tx, err := conn.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	fn(tx, New(tx))
}

func requirePqError(t *testing.T, err error, codeName string) {
	pqErr, ok := errors.AsType[*pq.Error](err)
	require.True(t, ok, "expected a PostgreSQL error, got %v", err)
	require.Equal(t, codeName, pqErr.Code.Name())
}

func TestCreateActivityType(t *testing.T) {
	user := createRandomUser(t)

	first := createRandomActivityType(t, user.ID)
	second := createRandomActivityType(t, user.ID)

	// personal items come after the standard ones
	require.Equal(t, int32(101), first.Position)
	require.Equal(t, int32(102), second.Position)
}

func TestCreateActivityTypeDuplicateCode(t *testing.T) {
	user := createRandomUser(t)
	activityType := createRandomActivityType(t, user.ID)

	params := CreateActivityTypeParams{
		UserID:         user.ID,
		Code:           activityType.Code,
		Label:          faker.Word(),
		TracksDuration: true,
	}
	_, err := testQueries.CreateActivityType(context.Background(), params)
	requirePqError(t, err, "unique_violation")

	// another user can use the same code
	otherUser := createRandomUser(t)
	params.UserID = otherUser.ID
	_, err = testQueries.CreateActivityType(context.Background(), params)
	require.NoError(t, err)
}

func TestCreateActivityTypeWithoutMeasure(t *testing.T) {
	user := createRandomUser(t)

	_, err := testQueries.CreateActivityType(context.Background(), CreateActivityTypeParams{
		UserID: user.ID,
		Code:   randomActivityCode(),
		Label:  faker.Word(),
	})
	requirePqError(t, err, "check_violation")
}

func TestGetUserActivityTypes(t *testing.T) {
	user := createRandomUser(t)
	otherUser := createRandomUser(t)

	kept := createRandomActivityType(t, user.ID)
	archived := createRandomActivityType(t, user.ID)
	someoneElses := createRandomActivityType(t, otherUser.ID)

	rows, err := testQueries.ArchiveActivityType(context.Background(), ArchiveActivityTypeParams{
		ID:     archived.ID,
		UserID: user.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), rows)

	activityTypes, err := testQueries.GetUserActivityTypes(context.Background(), user.ID)
	require.NoError(t, err)

	ids := make(map[int64]bool)
	for _, activityType := range activityTypes {
		ids[activityType.ID] = true
		// only standard items or this user's own items
		require.True(t, !activityType.UserID.Valid || activityType.UserID.Int64 == user.ID)
	}
	require.True(t, ids[kept.ID])
	require.False(t, ids[archived.ID])
	require.False(t, ids[someoneElses.ID])
}

func TestStandardActivityTypes(t *testing.T) {
	user := createRandomUser(t)
	ctx := context.Background()

	withRollback(t, func(tx *sql.Tx, q *Queries) {
		code := randomActivityCode()
		var standardID int64
		err := tx.QueryRowContext(ctx, `
			INSERT INTO activity_types (code, label, tracks_quantity, quantity_unit, position)
			VALUES ($1, 'Lecture biblique', TRUE, 'chapitres', 0)
			RETURNING id
		`, code).Scan(&standardID)
		require.NoError(t, err)

		personal, err := q.CreateActivityType(ctx, CreateActivityTypeParams{
			UserID:         user.ID,
			Code:           randomActivityCode(),
			Label:          faker.Word(),
			TracksDuration: true,
		})
		require.NoError(t, err)

		// the standard item is listed for every user, before the personal ones
		activityTypes, err := q.GetUserActivityTypes(ctx, user.ID)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(activityTypes), 2)
		require.Equal(t, standardID, activityTypes[0].ID)
		require.False(t, activityTypes[0].UserID.Valid)
		require.Equal(t, personal.ID, activityTypes[len(activityTypes)-1].ID)

		isStandard, err := q.IsStandardActivityCode(ctx, code)
		require.NoError(t, err)
		require.True(t, isStandard)

		isStandard, err = q.IsStandardActivityCode(ctx, personal.Code)
		require.NoError(t, err)
		require.False(t, isStandard)

		// a user can neither update nor archive a standard item
		_, err = q.UpdateActivityType(ctx, UpdateActivityTypeParams{
			ID:     standardID,
			UserID: user.ID,
			Code:   "HACK",
			Label:  "hack",
		})
		require.ErrorIs(t, err, sql.ErrNoRows)

		rows, err := q.ArchiveActivityType(ctx, ArchiveActivityTypeParams{
			ID:     standardID,
			UserID: user.ID,
		})
		require.NoError(t, err)
		require.Zero(t, rows)
	})
}

func TestUpdateActivityType(t *testing.T) {
	user := createRandomUser(t)
	activityType := createRandomActivityType(t, user.ID)

	params := UpdateActivityTypeParams{
		ID:     activityType.ID,
		UserID: user.ID,
		Code:   randomActivityCode(),
		Label:  "Évangélisation",
		QuantityUnit: sql.NullString{
			String: "personnes",
			Valid:  true,
		},
	}

	updated, err := testQueries.UpdateActivityType(context.Background(), params)
	require.NoError(t, err)
	require.Equal(t, params.Code, updated.Code)
	require.Equal(t, params.Label, updated.Label)
	require.Equal(t, params.QuantityUnit, updated.QuantityUnit)
	// no position sent: the current one is kept
	require.Equal(t, activityType.Position, updated.Position)
	// the measures never change
	require.Equal(t, activityType.TracksQuantity, updated.TracksQuantity)
	require.Equal(t, activityType.TracksDuration, updated.TracksDuration)

	params.Position = sql.NullInt32{Int32: 150, Valid: true}
	updated, err = testQueries.UpdateActivityType(context.Background(), params)
	require.NoError(t, err)
	require.Equal(t, int32(150), updated.Position)

	// another user can't touch it
	otherUser := createRandomUser(t)
	params.UserID = otherUser.ID
	_, err = testQueries.UpdateActivityType(context.Background(), params)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestUpdateActivityTypeUnitWithoutQuantity(t *testing.T) {
	user := createRandomUser(t)

	durationOnly, err := testQueries.CreateActivityType(context.Background(), CreateActivityTypeParams{
		UserID:         user.ID,
		Code:           randomActivityCode(),
		Label:          faker.Word(),
		TracksDuration: true,
	})
	require.NoError(t, err)

	_, err = testQueries.UpdateActivityType(context.Background(), UpdateActivityTypeParams{
		ID:     durationOnly.ID,
		UserID: user.ID,
		Code:   durationOnly.Code,
		Label:  durationOnly.Label,
		QuantityUnit: sql.NullString{
			String: "minutes",
			Valid:  true,
		},
	})
	requirePqError(t, err, "check_violation")
}

func TestArchiveActivityType(t *testing.T) {
	user := createRandomUser(t)
	otherUser := createRandomUser(t)
	activityType := createRandomActivityType(t, user.ID)

	// someone else's item: nothing archived
	rows, err := testQueries.ArchiveActivityType(context.Background(), ArchiveActivityTypeParams{
		ID:     activityType.ID,
		UserID: otherUser.ID,
	})
	require.NoError(t, err)
	require.Zero(t, rows)

	rows, err = testQueries.ArchiveActivityType(context.Background(), ArchiveActivityTypeParams{
		ID:     activityType.ID,
		UserID: user.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), rows)

	// already archived
	rows, err = testQueries.ArchiveActivityType(context.Background(), ArchiveActivityTypeParams{
		ID:     activityType.ID,
		UserID: user.ID,
	})
	require.NoError(t, err)
	require.Zero(t, rows)

	// an archived item can't be updated and keeps its code reserved
	_, err = testQueries.UpdateActivityType(context.Background(), UpdateActivityTypeParams{
		ID:     activityType.ID,
		UserID: user.ID,
		Code:   activityType.Code,
		Label:  "renamed",
	})
	require.ErrorIs(t, err, sql.ErrNoRows)

	_, err = testQueries.CreateActivityType(context.Background(), CreateActivityTypeParams{
		UserID:         user.ID,
		Code:           activityType.Code,
		Label:          faker.Word(),
		TracksDuration: true,
	})
	requirePqError(t, err, "unique_violation")
}
