package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Why a proclamation or an answer is refused. The API answers them with their own codes.
var (
	ErrImportunityNotFound = errors.New("importunity not found")
	ErrImportunityClosed   = errors.New("importunity closed")
	ErrNotParticipant      = errors.New("not a participant")
)

// SavePrayerTopicTx creates the topic ([id] 0) or changes it, with all its
// points: everything is saved or nothing is.
func (s *SQLStore) SavePrayerTopicTx(ctx context.Context, id int64, arg CreatePrayerTopicParams, points []string) (PrayerTopic, error) {
	var topic PrayerTopic
	err := s.execTx(ctx, func(q *Queries) error {
		var err error
		if id == 0 {
			topic, err = q.CreatePrayerTopic(ctx, arg)
		} else {
			topic, err = q.UpdatePrayerTopic(ctx, UpdatePrayerTopicParams{
				ID:           id,
				Title:        arg.Title,
				Category:     arg.Category,
				Verse:        arg.Verse,
				StartsOn:     arg.StartsOn,
				EndsOn:       arg.EndsOn,
				EventID:      arg.EventID,
				DocumentID:   arg.DocumentID,
				DocumentName: arg.DocumentName,
				Status:       arg.Status,
			})
		}
		if err != nil {
			return err
		}
		if err := q.DeletePrayerTopicPoints(ctx, topic.ID); err != nil {
			return err
		}
		for i, body := range points {
			if err := q.CreatePrayerTopicPoint(ctx, CreatePrayerTopicPointParams{
				TopicID:  topic.ID,
				Position: int32(i + 1),
				Body:     body,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return topic, err
}

// SetMyPrayedPointsTx replaces the points a member prayed for on [day].
func (s *SQLStore) SetMyPrayedPointsTx(ctx context.Context, topicID, userID int64, day time.Time, positions []int32) error {
	return s.execTx(ctx, func(q *Queries) error {
		if err := q.DeleteMyPrayedPoints(ctx, DeleteMyPrayedPointsParams{TopicID: topicID, UserID: userID, Day: day}); err != nil {
			return err
		}
		for _, position := range positions {
			if err := q.AddMyPrayedPoint(ctx, AddMyPrayedPointParams{
				TopicID:  topicID,
				Position: position,
				UserID:   userID,
				PrayedOn: day,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// CreateImportunityTx creates the topic; its author is its first participant.
func (s *SQLStore) CreateImportunityTx(ctx context.Context, arg CreateImportunityParams) (Importunity, error) {
	var importunity Importunity
	err := s.execTx(ctx, func(q *Queries) error {
		var err error
		importunity, err = q.CreateImportunity(ctx, arg)
		if err != nil {
			return err
		}
		return q.AddImportunityOwner(ctx, AddImportunityOwnerParams{ImportunityID: importunity.ID, UserID: arg.OwnerID})
	})
	return importunity, err
}

// UpdateImportunityTx changes the topic. The goal reached is dated again
// when the goal changes (a lower goal may already be reached).
func (s *SQLStore) UpdateImportunityTx(ctx context.Context, arg UpdateImportunityParams) (Importunity, error) {
	var importunity Importunity
	err := s.execTx(ctx, func(q *Queries) error {
		current, err := q.LockImportunity(ctx, arg.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrImportunityNotFound
		}
		if err != nil {
			return err
		}
		arg.ReachedAt = sql.NullTime{}
		if arg.Goal.Valid && current.Total >= arg.Goal.Int64 {
			arg.ReachedAt = current.ReachedAt
			if !arg.ReachedAt.Valid || current.Goal != arg.Goal {
				arg.ReachedAt = sql.NullTime{Time: time.Now(), Valid: true}
			}
		}
		importunity, err = q.UpdateImportunity(ctx, arg)
		return err
	})
	return importunity, err
}

// AddProclamationsTx counts [amount] proclamations of a participant: theirs
// and the total. The topic is locked so the goal is reached once.
func (s *SQLStore) AddProclamationsTx(ctx context.Context, importunityID, userID, amount int64) (Importunity, ImportunityMember, error) {
	var importunity Importunity
	var member ImportunityMember
	err := s.execTx(ctx, func(q *Queries) error {
		current, err := q.LockImportunity(ctx, importunityID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrImportunityNotFound
		}
		if err != nil {
			return err
		}
		if current.Status != "open" {
			return ErrImportunityClosed
		}
		member, err = q.AddMemberProclamations(ctx, AddMemberProclamationsParams{
			Amount:        amount,
			ImportunityID: importunityID,
			UserID:        userID,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotParticipant
		}
		if err != nil {
			return err
		}
		importunity, err = q.AddImportunityProclamations(ctx, AddImportunityProclamationsParams{Amount: amount, ID: importunityID})
		return err
	})
	return importunity, member, err
}
