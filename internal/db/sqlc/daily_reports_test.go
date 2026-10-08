package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/go-faker/faker/v4"
	"github.com/stretchr/testify/require"
)

var (
	reportDay1 = time.Date(2026, time.October, 8, 0, 0, 0, 0, time.UTC)
	reportDay2 = time.Date(2026, time.October, 9, 0, 0, 0, 0, time.UTC)
)

func validInt32(v int32) sql.NullInt32 {
	return sql.NullInt32{Int32: v, Valid: true}
}

// standardActivityType returns a standard item seeded by migration 000012 (LB, PS...).
func standardActivityType(t *testing.T, userID int64, code string) ActivityType {
	activityTypes, err := testQueries.GetUserActivityTypes(context.Background(), userID)
	require.NoError(t, err)

	for _, activityType := range activityTypes {
		if activityType.Code == code && !activityType.UserID.Valid {
			return activityType
		}
	}
	t.Fatalf("standard activity type %s not found: run the migrations", code)
	return ActivityType{}
}

func dailyReportFormByCode(t *testing.T, userID int64, date time.Time) map[string]GetDailyReportFormRow {
	rows, err := testQueries.GetDailyReportForm(context.Background(), GetDailyReportFormParams{
		UserID:     userID,
		ReportDate: date,
	})
	require.NoError(t, err)

	form := make(map[string]GetDailyReportFormRow, len(rows))
	for _, row := range rows {
		form[row.Code] = row
	}
	return form
}

func TestSaveDailyReportTx(t *testing.T) {
	store := NewSQLStore(testQueries.db.(*sql.DB))
	user := createRandomUser(t)
	lb := standardActivityType(t, user.ID, "LB")
	ps := standardActivityType(t, user.ID, "PS")
	personal := createRandomActivityType(t, user.ID)

	report, err := store.SaveDailyReportTx(context.Background(), SaveDailyReportTxParams{
		UserID:     user.ID,
		ReportDate: reportDay2,
		Note:       sql.NullString{String: faker.Sentence(), Valid: true},
		Entries: []ReportEntryInput{
			{ActivityTypeID: lb.ID, Quantity: validInt32(10)},
			{ActivityTypeID: ps.ID, DurationMinutes: validInt32(120)},
			{ActivityTypeID: personal.ID, Quantity: validInt32(1), DurationMinutes: validInt32(30)},
		},
	})
	require.NoError(t, err)
	require.NotZero(t, report.ID)
	require.Equal(t, user.ID, report.UserID)
	require.True(t, report.Note.Valid)
	require.Equal(t, reportDay2.Format("2006-01-02"), report.ReportDate.Format("2006-01-02"))

	form := dailyReportFormByCode(t, user.ID, reportDay2)
	require.Equal(t, validInt32(10), form["LB"].Quantity)
	require.False(t, form["LB"].DurationMinutes.Valid)
	require.Equal(t, validInt32(120), form["PS"].DurationMinutes)
	require.Equal(t, validInt32(1), form[personal.Code].Quantity)
	require.Equal(t, validInt32(30), form[personal.Code].DurationMinutes)

	// items that were not filled in are listed with no value
	for code, row := range form {
		if code != "LB" && code != "PS" && code != personal.Code {
			require.False(t, row.Quantity.Valid)
			require.False(t, row.DurationMinutes.Valid)
		}
	}
}

func TestSaveDailyReportTxReplacesTheDay(t *testing.T) {
	store := NewSQLStore(testQueries.db.(*sql.DB))
	user := createRandomUser(t)
	lb := standardActivityType(t, user.ID, "LB")
	ps := standardActivityType(t, user.ID, "PS")

	first, err := store.SaveDailyReportTx(context.Background(), SaveDailyReportTxParams{
		UserID:     user.ID,
		ReportDate: reportDay2,
		Entries: []ReportEntryInput{
			{ActivityTypeID: lb.ID, Quantity: validInt32(10)},
			{ActivityTypeID: ps.ID, DurationMinutes: validInt32(120)},
		},
	})
	require.NoError(t, err)

	second, err := store.SaveDailyReportTx(context.Background(), SaveDailyReportTxParams{
		UserID:     user.ID,
		ReportDate: reportDay2,
		Note:       sql.NullString{String: "corrigé", Valid: true},
		Entries: []ReportEntryInput{
			{ActivityTypeID: ps.ID, DurationMinutes: validInt32(135)},
		},
	})
	require.NoError(t, err)

	// same day, same report: the note is updated and LB is gone
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, "corrigé", second.Note.String)

	form := dailyReportFormByCode(t, user.ID, reportDay2)
	require.False(t, form["LB"].Quantity.Valid)
	require.Equal(t, validInt32(135), form["PS"].DurationMinutes)
}

func TestSaveDailyReportTxInvalidEntries(t *testing.T) {
	store := NewSQLStore(testQueries.db.(*sql.DB))
	user := createRandomUser(t)
	otherUser := createRandomUser(t)
	lb := standardActivityType(t, user.ID, "LB")
	ps := standardActivityType(t, user.ID, "PS")
	someoneElses := createRandomActivityType(t, otherUser.ID)

	archived := createRandomActivityType(t, user.ID)
	_, err := testQueries.ArchiveActivityType(context.Background(), ArchiveActivityTypeParams{
		ID:     archived.ID,
		UserID: user.ID,
	})
	require.NoError(t, err)

	testCases := []struct {
		name    string
		entries []ReportEntryInput
	}{
		{"someone else's item", []ReportEntryInput{{ActivityTypeID: someoneElses.ID, Quantity: validInt32(1)}}},
		{"archived item", []ReportEntryInput{{ActivityTypeID: archived.ID, Quantity: validInt32(1)}}},
		{"unknown item", []ReportEntryInput{{ActivityTypeID: 999_999_999, Quantity: validInt32(1)}}},
		{"quantity on a duration item", []ReportEntryInput{{ActivityTypeID: ps.ID, Quantity: validInt32(3)}}},
		{"duration on a quantity item", []ReportEntryInput{{ActivityTypeID: lb.ID, DurationMinutes: validInt32(30)}}},
		{"no value", []ReportEntryInput{{ActivityTypeID: lb.ID}}},
		{"same item twice", []ReportEntryInput{
			{ActivityTypeID: lb.ID, Quantity: validInt32(1)},
			{ActivityTypeID: lb.ID, Quantity: validInt32(2)},
		}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.SaveDailyReportTx(context.Background(), SaveDailyReportTxParams{
				UserID:     user.ID,
				ReportDate: reportDay2,
				Entries:    tc.entries,
			})
			require.ErrorIs(t, err, ErrInvalidReportEntry)
		})
	}

	// nothing was saved
	_, err = testQueries.GetDailyReport(context.Background(), GetDailyReportParams{
		UserID:     user.ID,
		ReportDate: reportDay2,
	})
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestListDailyReportEntries(t *testing.T) {
	store := NewSQLStore(testQueries.db.(*sql.DB))
	user := createRandomUser(t)
	otherUser := createRandomUser(t)
	lb := standardActivityType(t, user.ID, "LB")
	ps := standardActivityType(t, user.ID, "PS")

	noteOnlyDay := time.Date(2026, time.October, 5, 0, 0, 0, 0, time.UTC)
	days := []SaveDailyReportTxParams{
		{UserID: user.ID, ReportDate: noteOnlyDay, Note: sql.NullString{String: "malade", Valid: true}},
		{UserID: user.ID, ReportDate: reportDay1, Entries: []ReportEntryInput{{ActivityTypeID: lb.ID, Quantity: validInt32(5)}}},
		{UserID: user.ID, ReportDate: reportDay2, Entries: []ReportEntryInput{
			{ActivityTypeID: lb.ID, Quantity: validInt32(10)},
			{ActivityTypeID: ps.ID, DurationMinutes: validInt32(120)},
		}},
		{UserID: otherUser.ID, ReportDate: reportDay2, Entries: []ReportEntryInput{{ActivityTypeID: lb.ID, Quantity: validInt32(7)}}},
	}
	for _, day := range days {
		_, err := store.SaveDailyReportTx(context.Background(), day)
		require.NoError(t, err)
	}

	rows, err := testQueries.ListDailyReportEntries(context.Background(), ListDailyReportEntriesParams{
		UserID:   user.ID,
		FromDate: time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC),
		ToDate:   time.Date(2026, time.October, 31, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	// 2 entries on day 2, 1 on day 1, 1 row without entry for the note-only day; newest first
	require.Len(t, rows, 4)
	require.Equal(t, "2026-10-09", rows[0].ReportDate.Format("2006-01-02"))
	require.Equal(t, "2026-10-09", rows[1].ReportDate.Format("2006-01-02"))
	require.Equal(t, "2026-10-08", rows[2].ReportDate.Format("2006-01-02"))
	require.Equal(t, "2026-10-05", rows[3].ReportDate.Format("2006-01-02"))
	require.False(t, rows[3].ActivityTypeID.Valid)
	require.Equal(t, "malade", rows[3].Note.String)

	// the other user's LB 7 never shows up
	for _, row := range rows {
		require.NotEqual(t, validInt32(7), row.Quantity)
	}
}

func TestGetReportSummary(t *testing.T) {
	store := NewSQLStore(testQueries.db.(*sql.DB))
	user := createRandomUser(t)
	otherUser := createRandomUser(t)
	lb := standardActivityType(t, user.ID, "LB")
	ps := standardActivityType(t, user.ID, "PS")

	days := []SaveDailyReportTxParams{
		{UserID: user.ID, ReportDate: reportDay1, Entries: []ReportEntryInput{
			{ActivityTypeID: lb.ID, Quantity: validInt32(5)},
			{ActivityTypeID: ps.ID, DurationMinutes: validInt32(45)},
		}},
		{UserID: user.ID, ReportDate: reportDay2, Entries: []ReportEntryInput{
			{ActivityTypeID: lb.ID, Quantity: validInt32(10)},
			{ActivityTypeID: ps.ID, DurationMinutes: validInt32(120)},
		}},
		{UserID: otherUser.ID, ReportDate: reportDay2, Entries: []ReportEntryInput{{ActivityTypeID: lb.ID, Quantity: validInt32(7)}}},
	}
	for _, day := range days {
		_, err := store.SaveDailyReportTx(context.Background(), day)
		require.NoError(t, err)
	}

	from := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.October, 31, 0, 0, 0, 0, time.UTC)

	daysReported, err := testQueries.CountReportedDays(context.Background(), CountReportedDaysParams{
		UserID:   user.ID,
		FromDate: from,
		ToDate:   to,
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), daysReported)

	rows, err := testQueries.GetReportSummary(context.Background(), GetReportSummaryParams{
		UserID:   user.ID,
		FromDate: from,
		ToDate:   to,
	})
	require.NoError(t, err)

	totals := make(map[string]GetReportSummaryRow, len(rows))
	for _, row := range rows {
		totals[row.Code] = row
	}

	// the other user's LB 7 is not counted
	require.Equal(t, int64(2), totals["LB"].DaysCount)
	require.Equal(t, int64(15), totals["LB"].TotalQuantity)
	require.Equal(t, int64(2), totals["PS"].DaysCount)
	require.Equal(t, int64(165), totals["PS"].TotalMinutes)

	// unused items are listed with zeros, not left out
	require.Contains(t, totals, "LLC")
	require.Zero(t, totals["LLC"].DaysCount)
	require.Zero(t, totals["LLC"].TotalQuantity)
}
