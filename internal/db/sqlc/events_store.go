package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Why a registration is refused. The API answers them with their own codes.
var (
	ErrEventNotFound      = errors.New("event not found")
	ErrRegistrationClosed = errors.New("registration closed")
	ErrEventFull          = errors.New("event full")
)

// RegistrationState tells whether members can register to [event] at [now],
// given [people] already registered (all registrations together).
func RegistrationState(event Event, now time.Time, people int32) string {
	switch {
	case event.Status == "cancelled":
		return "cancelled"
	case !event.EndsAt.After(now):
		return "ended"
	case event.Status != "published" || !event.RegistrationOpen:
		return "closed"
	case event.RegistrationDeadline.Valid && !event.RegistrationDeadline.Time.After(now):
		return "closed"
	case event.Capacity.Valid && people >= event.Capacity.Int32:
		return "full"
	default:
		return "open"
	}
}

// RegisterForEventTx creates or changes the registration of a member. The
// event row is locked while the places are counted, so two people can't take
// the last place at the same time. A member already registered may still
// change their registration when the event is full, within the places they hold.
func (s *SQLStore) RegisterForEventTx(ctx context.Context, arg UpsertEventRegistrationParams, now time.Time) (EventRegistration, error) {
	var registration EventRegistration
	err := s.execTx(ctx, func(q *Queries) error {
		event, err := q.LockEvent(ctx, arg.EventID)
		if errors.Is(err, sql.ErrNoRows) || err == nil && event.Status == "draft" {
			return ErrEventNotFound
		}
		if err != nil {
			return err
		}
		others, err := q.SumOtherRegistrationsPeople(ctx, SumOtherRegistrationsPeopleParams{
			EventID: arg.EventID,
			UserID:  arg.UserID,
		})
		if err != nil {
			return err
		}
		switch RegistrationState(event, now, others) {
		case "open":
		case "full":
			return ErrEventFull
		default:
			return ErrRegistrationClosed
		}
		if event.Capacity.Valid && others+arg.People > event.Capacity.Int32 {
			return ErrEventFull
		}
		registration, err = q.UpsertEventRegistration(ctx, arg)
		return err
	})
	return registration, err
}

// ReplaceEventSessionsTx replaces the whole program of an event.
func (s *SQLStore) ReplaceEventSessionsTx(ctx context.Context, eventID int64, sessions []CreateEventSessionParams) ([]EventSession, error) {
	created := make([]EventSession, 0, len(sessions))
	err := s.execTx(ctx, func(q *Queries) error {
		if err := q.DeleteEventSessions(ctx, eventID); err != nil {
			return err
		}
		for _, session := range sessions {
			session.EventID = eventID
			row, err := q.CreateEventSession(ctx, session)
			if err != nil {
				return err
			}
			created = append(created, row)
		}
		return nil
	})
	return created, err
}

// DuplicateEventTx prepares the next edition of a yearly event: a draft one
// year later, with the same place, poster, settings and program. Registrations,
// news and photos stay with the edition they belong to. Both editions join
// the same series (made from the first one's title when there is none yet).
func (s *SQLStore) DuplicateEventTx(ctx context.Context, eventID int64, createdBy int64) (Event, error) {
	var next Event
	err := s.execTx(ctx, func(q *Queries) error {
		event, err := q.GetEvent(ctx, eventID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrEventNotFound
		}
		if err != nil {
			return err
		}

		if !event.SeriesID.Valid {
			series, err := q.CreateEventSeries(ctx, event.Title)
			if err != nil {
				return err
			}
			event.SeriesID = sql.NullInt64{Int64: series.ID, Valid: true}
			if err := q.SetEventSeries(ctx, SetEventSeriesParams{ID: event.ID, SeriesID: event.SeriesID}); err != nil {
				return err
			}
		}

		// the same local date and time, a year later, in the place's time zone
		location, err := time.LoadLocation(event.TimeZone)
		if err != nil {
			return fmt.Errorf("event %d: %w", event.ID, err)
		}
		nextYear := func(t time.Time) time.Time { return t.In(location).AddDate(1, 0, 0) }
		deadline := event.RegistrationDeadline
		if deadline.Valid {
			deadline.Time = nextYear(deadline.Time)
		}

		next, err = q.CreateEvent(ctx, CreateEventParams{
			SeriesID:             event.SeriesID,
			Title:                event.Title,
			Theme:                event.Theme,
			ThemeVerse:           event.ThemeVerse,
			Description:          event.Description,
			StartsAt:             nextYear(event.StartsAt),
			EndsAt:               nextYear(event.EndsAt),
			TimeZone:             event.TimeZone,
			Venue:                event.Venue,
			Address:              event.Address,
			City:                 event.City,
			Country:              event.Country,
			MapsUrl:              event.MapsUrl,
			ContactName:          event.ContactName,
			ContactPhone:         event.ContactPhone,
			PosterID:             event.PosterID,
			RegistrationOpen:     event.RegistrationOpen,
			RegistrationDeadline: deadline,
			Capacity:             event.Capacity,
			AsksLodging:          event.AsksLodging,
			AsksDepartureCity:    event.AsksDepartureCity,
			FeeNote:              event.FeeNote,
			Status:               "draft",
			CreatedBy:            sql.NullInt64{Int64: createdBy, Valid: createdBy != 0},
		})
		if err != nil {
			return err
		}

		sessions, err := q.ListEventSessions(ctx, event.ID)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			endsAt := session.EndsAt
			if endsAt.Valid {
				endsAt.Time = nextYear(endsAt.Time)
			}
			_, err := q.CreateEventSession(ctx, CreateEventSessionParams{
				EventID:     next.ID,
				StartsAt:    nextYear(session.StartsAt),
				EndsAt:      endsAt,
				Title:       session.Title,
				Speaker:     session.Speaker,
				Place:       session.Place,
				Description: session.Description,
				Hymns:       session.Hymns,
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return next, err
}
