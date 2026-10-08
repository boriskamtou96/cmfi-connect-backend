package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrInvalidReportEntry wraps every "this entry does not fit this item" error,
// so the API can answer 400 with errors.Is while keeping the detailed message.
var ErrInvalidReportEntry = errors.New("invalid report entry")

type SQLStore struct {
	*Queries
	db *sql.DB
}

func NewSQLStore(db *sql.DB) *SQLStore {
	return &SQLStore{
		db:      db,
		Queries: New(db),
	}
}

func (s *SQLStore) execTx(ctx context.Context, fn func(queries *Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	q := New(tx)
	err = fn(q)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("tx err: %v, rb err: %v", err, rbErr)
		}
		return err
	}
	return tx.Commit()
}

type ReportEntryInput struct {
	ActivityTypeID  int64
	Quantity        sql.NullInt32
	DurationMinutes sql.NullInt32
}

type SaveDailyReportTxParams struct {
	UserID     int64
	ReportDate time.Time
	Note       sql.NullString
	Entries    []ReportEntryInput
}

// SaveDailyReportTx replaces the whole day in one transaction: everything is saved or nothing is.
func (s *SQLStore) SaveDailyReportTx(ctx context.Context, arg SaveDailyReportTxParams) (DailyReport, error) {
	var report DailyReport

	err := s.execTx(ctx, func(q *Queries) error {
		// 1. every entry must use an item this user can fill in, with the right measures
		activityTypes, err := q.GetUserActivityTypes(ctx, arg.UserID)
		if err != nil {
			return err
		}

		available := make(map[int64]ActivityType, len(activityTypes))
		for _, activityType := range activityTypes {
			available[activityType.ID] = activityType
		}

		if err := checkReportEntries(arg.Entries, available); err != nil {
			return err
		}

		// 2. create the day, or update its note
		report, err = q.UpsertDailyReport(ctx, UpsertDailyReportParams{
			UserID:     arg.UserID,
			ReportDate: arg.ReportDate,
			Note:       arg.Note,
		})
		if err != nil {
			return err
		}

		// 3. replace the day's entries
		if err := q.DeleteReportEntries(ctx, report.ID); err != nil {
			return err
		}

		for _, entry := range arg.Entries {
			err := q.CreateReportEntry(ctx, CreateReportEntryParams{
				ReportID:        report.ID,
				ActivityTypeID:  entry.ActivityTypeID,
				Quantity:        entry.Quantity,
				DurationMinutes: entry.DurationMinutes,
			})
			if err != nil {
				return err
			}
		}

		return nil
	})

	return report, err
}

func checkReportEntries(entries []ReportEntryInput, available map[int64]ActivityType) error {
	seen := make(map[int64]bool, len(entries))

	for _, entry := range entries {
		activityType, ok := available[entry.ActivityTypeID]
		if !ok {
			return fmt.Errorf("%w: activity type %d is not available", ErrInvalidReportEntry, entry.ActivityTypeID)
		}
		if seen[entry.ActivityTypeID] {
			return fmt.Errorf("%w: activity type %d (%s) appears twice", ErrInvalidReportEntry, activityType.ID, activityType.Code)
		}
		seen[entry.ActivityTypeID] = true

		if !entry.Quantity.Valid && !entry.DurationMinutes.Valid {
			return fmt.Errorf("%w: activity type %d (%s) needs a quantity or a duration", ErrInvalidReportEntry, activityType.ID, activityType.Code)
		}
		if entry.Quantity.Valid && !activityType.TracksQuantity {
			return fmt.Errorf("%w: activity type %d (%s) does not track a quantity", ErrInvalidReportEntry, activityType.ID, activityType.Code)
		}
		if entry.DurationMinutes.Valid && !activityType.TracksDuration {
			return fmt.Errorf("%w: activity type %d (%s) does not track a duration", ErrInvalidReportEntry, activityType.ID, activityType.Code)
		}
	}

	return nil
}
