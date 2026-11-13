package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ErrGameCoursePlayed: the course has lessons members already finished, so an
// import (which replaces its levels) would erase their progress.
var ErrGameCoursePlayed = errors.New("the course was already played")

// A course to import, texts already checked and encoded ({"fr": "…"}).
type GameCourseImport struct {
	Code      string
	Position  int32
	Title     json.RawMessage
	Published bool
	Levels    []GameLevelImport
}

type GameLevelImport struct {
	Title, Description json.RawMessage
	Color              string
	Lessons            []GameLessonImport
}

type GameLessonImport struct {
	Title     json.RawMessage
	Questions []CreateGameQuestionParams
}

type GameCourseImportResult struct {
	Levels, Lessons, Questions int
}

// ImportGameCourseTx writes the course and replaces its levels, in the order
// given. Refused with ErrGameCoursePlayed once a member finished one of its
// lessons.
func (s *SQLStore) ImportGameCourseTx(ctx context.Context, course GameCourseImport, now time.Time) (GameCourseImportResult, error) {
	var result GameCourseImportResult
	published := sql.NullTime{Time: now, Valid: course.Published}

	err := s.execTx(ctx, func(q *Queries) error {
		played, err := q.CountGameResultsOfCourse(ctx, course.Code)
		if err != nil {
			return err
		}
		if played > 0 {
			return ErrGameCoursePlayed
		}
		courseID, err := q.UpsertGameCourse(ctx, UpsertGameCourseParams{
			Code: course.Code, Position: course.Position, Title: course.Title, PublishedAt: published,
		})
		if err != nil {
			return err
		}
		if err := q.DeleteGameLevelsOfCourse(ctx, courseID); err != nil {
			return err
		}
		for l, level := range course.Levels {
			levelID, err := q.CreateGameLevel(ctx, CreateGameLevelParams{
				CourseID: courseID, Position: int32(l + 1), Title: level.Title,
				Description: level.Description, Color: level.Color, PublishedAt: published,
			})
			if err != nil {
				return err
			}
			result.Levels++
			for n, lesson := range level.Lessons {
				lessonID, err := q.CreateGameLesson(ctx, CreateGameLessonParams{
					LevelID: levelID, Position: int32(n + 1), Title: lesson.Title, PublishedAt: published,
				})
				if err != nil {
					return err
				}
				result.Lessons++
				for p, question := range lesson.Questions {
					question.LessonID = lessonID
					question.Position = int32(p + 1)
					if err := q.CreateGameQuestion(ctx, question); err != nil {
						return err
					}
					result.Questions++
				}
			}
		}
		return nil
	})
	return result, err
}

// WithGamePlayerTx runs [fn] in a transaction with the player's line locked
// (made if they never played): two plays of one member wait in line, and a
// play sent twice is seen by the second.
func (s *SQLStore) WithGamePlayerTx(ctx context.Context, userID int64, fn func(q *Queries, player GamePlayer) error) error {
	return s.execTx(ctx, func(q *Queries) error {
		if err := q.EnsureGamePlayer(ctx, userID); err != nil {
			return err
		}
		player, err := q.LockGamePlayer(ctx, userID)
		if err != nil {
			return err
		}
		return fn(q, player)
	})
}

// ErrGameOrderOutOfDate: the ids to order are not those of the list today
// (someone added or took one out since it was read).
var ErrGameOrderOutOfDate = errors.New("the list changed since it was read")

// sameIDs: [wanted] holds every id of [current] once, and no other.
func sameIDs(current, wanted []int64) bool {
	if len(current) != len(wanted) {
		return false
	}
	left := make(map[int64]bool, len(current))
	for _, id := range current {
		left[id] = true
	}
	for _, id := range wanted {
		if !left[id] {
			return false
		}
		delete(left, id)
	}
	return true
}

// reorderGame writes the positions 1, 2, 3… in the order of [ids], once
// they are checked against the list read in the same transaction.
func (s *SQLStore) reorderGame(
	ctx context.Context, ids []int64,
	current func(q *Queries) ([]int64, error),
	setAside func(q *Queries) error,
	set func(q *Queries, id int64, position int32) error,
) error {
	return s.execTx(ctx, func(q *Queries) error {
		now, err := current(q)
		if err != nil {
			return err
		}
		if !sameIDs(now, ids) {
			return ErrGameOrderOutOfDate
		}
		if err := setAside(q); err != nil {
			return err
		}
		for i, id := range ids {
			if err := set(q, id, int32(i+1)); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReorderGameLevelsTx puts the levels of a course in the order of [ids].
func (s *SQLStore) ReorderGameLevelsTx(ctx context.Context, courseID int64, ids []int64) error {
	return s.reorderGame(ctx, ids,
		func(q *Queries) ([]int64, error) { return q.ListGameLevelIDs(ctx, courseID) },
		func(q *Queries) error { return q.SetAsideGameLevelPositions(ctx, courseID) },
		func(q *Queries, id int64, position int32) error {
			return q.SetGameLevelPosition(ctx, SetGameLevelPositionParams{ID: id, Position: position})
		})
}

// ReorderGameLessonsTx puts the lessons of a level in the order of [ids].
func (s *SQLStore) ReorderGameLessonsTx(ctx context.Context, levelID int64, ids []int64) error {
	return s.reorderGame(ctx, ids,
		func(q *Queries) ([]int64, error) { return q.ListGameLessonIDs(ctx, levelID) },
		func(q *Queries) error { return q.SetAsideGameLessonPositions(ctx, levelID) },
		func(q *Queries, id int64, position int32) error {
			return q.SetGameLessonPosition(ctx, SetGameLessonPositionParams{ID: id, Position: position})
		})
}

// ReorderGameQuestionsTx puts the questions of a lesson in the order of [ids].
func (s *SQLStore) ReorderGameQuestionsTx(ctx context.Context, lessonID int64, ids []int64) error {
	return s.reorderGame(ctx, ids,
		func(q *Queries) ([]int64, error) { return q.ListGameQuestionIDs(ctx, lessonID) },
		func(q *Queries) error { return nil },
		func(q *Queries, id int64, position int32) error {
			return q.SetGameQuestionPosition(ctx, SetGameQuestionPositionParams{ID: id, Position: position})
		})
}
