package api

import (
	"database/sql"
	"errors"
	"math/rand/v2"
	"net/http"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/games"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

// The challenge of the day: 5 questions drawn from the lessons the member
// finished, the same all day (Douala time), worth 15 points once a day.

type gameDailyResponse struct {
	// already done today
	Done      bool                   `json:"done"`
	Questions []gameQuestionResponse `json:"questions"`
}

type gameDailyResultResponse struct {
	Points   int                `json:"points"`
	FirstTry int                `json:"first_try"`
	Total    int                `json:"total"`
	LevelUp  bool               `json:"level_up"`
	Player   gamePlayerResponse `json:"player"`
}

const dailyQuestions = 5

var errNoLessonDone = errors.New("no lesson done yet")

// dailyChallenge draws the questions of [userID] for the day of [now] among
// those of the lessons they finished: the same draw all day, another one the
// next day. errNoLessonDone before their first lesson.
func (s *Server) dailyChallenge(c *gin.Context, userID int64, now time.Time) ([]gameQuestionRow, error) {
	rows, err := s.store.ListCompletedGameQuestions(c, userID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errNoLessonDone
	}
	draw := rand.New(rand.NewPCG(uint64(userID), uint64(games.GameDay(now).Unix())))
	questions := make([]gameQuestionRow, 0, dailyQuestions)
	for _, i := range draw.Perm(len(rows)) {
		if len(questions) == dailyQuestions {
			break
		}
		questions = append(questions, gameQuestionRow(rows[i]))
	}
	return questions, nil
}

// GET /v1/games/daily?lang=: the challenge of the day.
func (s *Server) getGameDaily(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	now := gameClock()
	questions, err := s.dailyChallenge(c, authUser.ID, now)
	if errors.Is(err, errNoLessonDone) {
		resourceNotFoundError(c, "challenge of the day")
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	player, err := s.store.GetGamePlayer(c, authUser.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, gameDailyResponse{
		Done:      sameDay(dateOf(player.DailyDoneOn), games.GameDay(now)),
		Questions: toGameQuestions(questions, gameLang(c)),
	})
}

// POST /v1/games/daily/results: the challenge of the day done. 15 points
// the first time of the day, nothing after; it counts for the streak.
func (s *Server) submitGameDaily(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	var request gamePlayRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequestError(c, err)
		return
	}
	answers, ok := request.read(c)
	if !ok {
		return
	}
	now := gameClock()
	today := games.GameDay(now)
	// any question of a lesson done counts: a challenge opened just before
	// midnight and sent after still holds yesterday's draw
	questions, err := s.store.ListCompletedGameQuestions(c, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	if len(questions) == 0 {
		resourceNotFoundError(c, "challenge of the day")
		return
	}
	key := make(map[int64]string, len(questions))
	for _, question := range questions {
		key[question.ID] = question.Answer
	}

	var play db.GamePlay
	err = s.store.WithGamePlayerTx(c, authUser.ID, func(q *db.Queries, player db.GamePlayer) error {
		var err error
		play, err = q.GetGamePlayByClientID(c, db.GetGamePlayByClientIDParams{UserID: authUser.ID, ClientID: request.ClientID})
		if err == nil {
			// already received
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		score, counted := games.ScoreLesson(answers, key)
		if !counted {
			return errNoGameAnswer
		}
		points := games.PointsDaily
		if sameDay(dateOf(player.DailyDoneOn), today) {
			points = 0
		}
		before, _, _ := games.PlayerLevel(int(player.TotalPoints))
		total := int(player.TotalPoints) + points
		after, _, _ := games.PlayerLevel(total)
		play, err = q.CreateGamePlay(c, db.CreateGamePlayParams{
			UserID: authUser.ID, Kind: "daily", ClientID: request.ClientID,
			FirstTry: int16(score.FirstTry), Total: int16(score.Total), Stars: int16(score.Stars),
			Points: int32(points), LevelUp: after > before, PlayedAt: now,
		})
		if err != nil {
			return err
		}
		streak := streakOf(player).After(today)
		return q.SaveGamePlayerProgress(c, db.SaveGamePlayerProgressParams{
			UserID: authUser.ID, TotalPoints: int32(total), StreakDays: int32(streak.Days),
			LastPlayedOn: nullDate(streak.LastPlayedOn), GraceUsedOn: nullDate(streak.GraceUsedOn),
			DailyDoneOn: sql.NullTime{Time: today, Valid: true},
		})
	})
	if errors.Is(err, errNoGameAnswer) {
		badRequestError(c, err)
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	player, err := s.gamePlayer(c, authUser.ID, now)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, gameDailyResultResponse{
		Points: int(play.Points), FirstTry: int(play.FirstTry), Total: int(play.Total), LevelUp: play.LevelUp, Player: player,
	})
}
