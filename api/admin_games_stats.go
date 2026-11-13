package api

import (
	"net/http"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/games"
	"github.com/gin-gonic/gin"
)

// The numbers of the games for the back-office. Never a name: counts only.

const (
	// a question says something once it was asked this many times
	hardGameQuestionAsked = 5
	hardGameQuestions     = 10
)

type gamePlayersStats struct {
	// those who won at least a point
	Total int `json:"total"`
	// those who played since Monday (Douala time)
	Week          int `json:"week"`
	InLeaderboard int `json:"in_leaderboard"`
}

type gameLessonsStats struct {
	Total int `json:"total"`
	Week  int `json:"week"`
}

// hardGameQuestion is a question often missed at the first answer: often a
// question badly asked.
type hardGameQuestion struct {
	QuestionID  int64  `json:"question_id"`
	LessonID    int64  `json:"lesson_id"`
	LessonTitle string `json:"lesson_title"`
	Course      string `json:"course"`
	Prompt      string `json:"prompt"`
	Asked       int    `json:"asked"`
	Missed      int    `json:"missed"`
}

type gameStatsResponse struct {
	Players          gamePlayersStats   `json:"players"`
	LessonsCompleted gameLessonsStats   `json:"lessons_completed"`
	PlaysWeek        int                `json:"plays_week"`
	Hardest          []hardGameQuestion `json:"hardest"`
}

// GET /v1/admin/games/stats
func (s *Server) getGameStats(c *gin.Context) {
	stats, err := s.store.GetGameStats(c, games.WeekStart(gameClock()))
	if err != nil {
		internalServerError(c, err)
		return
	}
	rows, err := s.store.ListHardestGameQuestions(c, db.ListHardestGameQuestionsParams{
		MinAsked: hardGameQuestionAsked, MaxRows: hardGameQuestions,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	response := gameStatsResponse{
		Players:          gamePlayersStats{Total: int(stats.Players), Week: int(stats.PlayersWeek), InLeaderboard: int(stats.InLeaderboard)},
		LessonsCompleted: gameLessonsStats{Total: int(stats.LessonsCompleted), Week: int(stats.LessonsCompletedWeek)},
		PlaysWeek:        int(stats.PlaysWeek),
		Hardest:          make([]hardGameQuestion, 0, len(rows)),
	}
	for _, row := range rows {
		response.Hardest = append(response.Hardest, hardGameQuestion{
			QuestionID: row.ID, LessonID: row.LessonID, LessonTitle: textIn(row.LessonTitle, gameDefaultLang),
			Course: row.CourseCode, Prompt: textIn(row.Prompt, gameDefaultLang),
			Asked: int(row.Asked), Missed: int(row.Missed),
		})
	}
	apiResponse(c, http.StatusOK, response)
}
